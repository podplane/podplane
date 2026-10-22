// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/secrets"
)

// TestInstallInitializesRequiredSecrets exercises the command against fake
// helm/kubectl processes that reject enabling Source Gateway before its key exists.
func TestInstallInitializesRequiredSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, component, status                  string
		operator, failCreate, wantErr, wantWrite bool
	}{
		{name: "new operator", component: "sourcegateway", wantWrite: true},
		{name: "existing key", component: "sourcegateway", operator: true, status: "active"},
		{name: "archived key", component: "sourcegateway", operator: true, status: "archived", wantErr: true},
		{name: "failed creation", component: "sourcegateway", operator: true, failCreate: true, wantErr: true, wantWrite: true},
		{name: "transitive dependency", component: "consumer", operator: true, wantWrite: true},
		{name: "unrelated component", component: "agent-router", operator: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, data []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
					t.Fatal(err)
				}
			}
			values := fmt.Sprintf(`{"platform":{"components":{
"apps":{
  "podplane-operator":{"enabled":%t,"namespace":"platform-podplane-operator"},
  "sourcegateway":{"enabled":false,"namespace":"custom-sourcegateway","dependsOn":["podplane-operator"]},
  "consumer":{"enabled":false,"namespace":"consumer","dependsOn":["sourcegateway"]},
  "agent-router":{"enabled":false,"namespace":"agent-router"}},
"values":{
  "sourcegateway":{"platform":{"sourceGateway":{"providerName":"alternate"}}},
  "podplane-operator":{"podplane":{"operator":{"config":{"cluster":{"id":"cluster"},"secrets":{"defaultProvider":"primary","providers":{"primary":{"kind":"aws","keyPrefix":"shared"}}}}}}}
}}}}`, tc.operator)
			if !json.Valid([]byte(values)) {
				t.Fatal("invalid Helm values fixture")
			}
			write("values", []byte(values))
			write("helm", []byte("#!/bin/sh\ncat \"$INSTALL_STATE/values\"\n"))
			key, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			pub, err := json.Marshal(secrets.PublicKey{Spec: secrets.PublicKeySpec{KeyID: "latest", Algorithm: secrets.Algorithm, PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}})
			if err != nil {
				t.Fatal(err)
			}
			write("public", pub)
			if tc.operator {
				write("operator-ready", nil)
			}
			if tc.status == "active" {
				write("key-active", nil)
			}
			write("kubectl", []byte(`#!/bin/sh
set -eu
case "$*" in
  *"config view"*) echo '{"current-context":"chosen","contexts":[{"name":"chosen","context":{"cluster":"podplane-cluster"}}]}';;
  *"patch helmrelease"*)
    case "$*" in
      *'"sourcegateway":{"enabled":true}'*)
        test -f "$INSTALL_STATE/key-active" || { echo 'workload enabled before key' >&2; exit 1; }
        touch "$INSTALL_STATE/gateway-enabled";;
    esac
    touch "$INSTALL_STATE/operator-ready"
    echo '{}';;
  *"get helmreleases.helm.toolkit.fluxcd.io"*)
    test -f "$INSTALL_STATE/operator-ready" || exit 1
    echo '{"items":[
      {"metadata":{"name":"podplane-operator","namespace":"platform-podplane-operator"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
      {"metadata":{"name":"sourcegateway","namespace":"custom-sourcegateway"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
      {"metadata":{"name":"consumer","namespace":"consumer"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
      {"metadata":{"name":"agent-router","namespace":"agent-router"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}
    ]}';;
  *" wait "*)
    test -f "$INSTALL_STATE/operator-ready" || exit 1
    touch "$INSTALL_STATE/api-ready";;
  *publickeys/latest*)
    test -f "$INSTALL_STATE/api-ready" || exit 1
    cat "$INSTALL_STATE/public";;
  *namespaces/custom-sourcegateway/secretproviderkeyspaces/primary.sourcegateway*)
    test -f "$INSTALL_STATE/api-ready" || exit 1
    if [ "$1" = replace ]; then
      cat > "$INSTALL_STATE/request"
      if [ "$INSTALL_FAIL" = true ]; then echo forbidden >&2; exit 1; fi
      touch "$INSTALL_STATE/key-active"
    fi
    status="$INSTALL_STATUS"
    if [ -f "$INSTALL_STATE/key-active" ]; then status=active; fi
    if [ -z "$status" ]; then echo '{}'; else
      printf '{"status":{"entries":[{"key":"encryption-key","status":"%s"}]}}' "$status"
    fi;;
  *) echo "unexpected invocation: $*" >&2; exit 1;;
esac
`))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("INSTALL_STATE", dir)
			t.Setenv("INSTALL_STATUS", tc.status)
			t.Setenv("INSTALL_FAIL", fmt.Sprint(tc.failCreate))
			command := newInstallCmd(nil)
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs([]string{tc.component, "--auto-approve"})
			err = command.Execute()
			if (err != nil) != tc.wantErr {
				t.Fatalf("install error = %v, want error=%t", err, tc.wantErr)
			}
			if tc.status == "archived" && !strings.Contains(err.Error(), "restore") {
				t.Fatalf("expected restoration guidance, got %v", err)
			}
			if tc.failCreate && !strings.Contains(err.Error(), "forbidden") {
				t.Fatalf("expected backend failure, got %v", err)
			}
			_, enabledErr := os.Stat(filepath.Join(dir, "gateway-enabled"))
			wantEnabled := !tc.wantErr && tc.component != "agent-router"
			if (enabledErr == nil) != wantEnabled {
				t.Fatalf("gateway enabled=%t, want %t", enabledErr == nil, wantEnabled)
			}
			request, readErr := os.ReadFile(filepath.Join(dir, "request"))
			if (readErr == nil) != tc.wantWrite {
				t.Fatalf("secret write=%t, want %t", readErr == nil, tc.wantWrite)
			}
			if tc.wantWrite {
				var body secrets.SecretProviderKeyspace
				if err := json.Unmarshal(request, &body); err != nil {
					t.Fatal(err)
				}
				if body.Metadata.Namespace != "custom-sourcegateway" || body.Metadata.Name != "primary.sourcegateway" || body.Spec == nil || len(body.Spec.Entries) != 1 || body.Spec.Entries[0].Operation != "create" || body.Spec.Entries[0].EncryptedValue == nil {
					t.Fatal("install did not issue a scoped, encrypted, create-only request")
				}
			}
		})
	}
}
