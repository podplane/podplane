// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// TestProvisionSpec checks provider-prefixed backend paths.
func TestProvisionSpec(t *testing.T) {
	r := ProvisionSpec{Namespace: "system", Binding: "netsy", Key: "token", Provider: clusterconfig.SecretsProvider{KeyPrefix: "cluster"}}
	if got := r.KeyPath(); got != "cluster/system/netsy/token" {
		t.Fatalf("KeyPath = %q", got)
	}
}

// TestEnsure checks preservation of active secrets, encrypted create-only writes,
// adoption of concurrent winners, and errors for inactive entries or API failures.
func TestEnsure(t *testing.T) {
	for _, tc := range []struct {
		name, initial, after, failure string
		wantErr, write                bool
	}{
		{name: "active", initial: "active"},
		{name: "active without generation", initial: "active", failure: "generate"},
		{name: "generation failure", failure: "generate", wantErr: true},
		{name: "archived", initial: "archived", wantErr: true},
		{name: "unknown", initial: "pending", wantErr: true},
		{name: "create", write: true},
		{name: "race winner", after: "active", failure: "backend", write: true},
		{name: "unrelated active", after: "other", failure: "backend", write: true, wantErr: true},
		{name: "backend failure", failure: "backend", write: true, wantErr: true},
		{name: "raced archive", after: "archived", failure: "backend", write: true, wantErr: true},
		{name: "reread failure", after: "error", failure: "backend", write: true, wantErr: true},
		{name: "initial read failure", initial: "error", wantErr: true},
		{name: "public key failure", failure: "public", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			key, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			pub := PublicKey{Spec: PublicKeySpec{KeyID: "latest", Algorithm: Algorithm, PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}}
			data, err := json.Marshal(pub)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "public"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
echo "$*" >> "$STATE/calls"
case "$*" in
  *publickeys/latest*)
    if [ "$FAILURE" = public ]; then echo forbidden >&2; exit 1; fi
    cat "$STATE/public"; exit 0;;
esac
if [ "$1" = replace ]; then
  cat > "$STATE/request"
  if [ "$FAILURE" = backend ]; then echo backend-unavailable >&2; exit 1; fi
  echo '{}'; exit 0
fi
status="$INITIAL"
if [ -f "$STATE/read" ]; then status="$AFTER"; fi
touch "$STATE/read"
if [ "$status" = error ]; then echo metadata-unavailable >&2; exit 1; fi
if [ -z "$status" ]; then echo '{}'; exit 0; fi
key=token
if [ "$status" = other ]; then key=other; status=active; fi
printf '{"status":{"entries":[{"key":"%s","status":"%s"}]}}' "$key" "$status"
`
			if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("STATE", dir)
			t.Setenv("INITIAL", tc.initial)
			t.Setenv("AFTER", tc.after)
			t.Setenv("FAILURE", tc.failure)
			const payload = "custom credential\nnot base64"
			r := ProvisionSpec{
				ProviderName: "bao", Namespace: "system", Binding: "netsy", Key: "token",
				Generate: func() ([]byte, error) {
					if tc.failure == "generate" {
						return nil, errors.New("generation failed")
					}
					return []byte(payload), nil
				},
			}
			err = (Client{}).Ensure("cluster", r)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Ensure = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.initial == "archived" && !strings.Contains(err.Error(), "restore") {
				t.Fatal("missing recovery instruction")
			}
			request, readErr := os.ReadFile(filepath.Join(dir, "request"))
			if !tc.write {
				if !os.IsNotExist(readErr) {
					t.Fatalf("unexpected write: %s, %v", request, readErr)
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			var got SecretProviderKeyspace
			if err := json.Unmarshal(request, &got); err != nil {
				t.Fatal(err)
			}
			if got.Metadata.Namespace != "system" || got.Metadata.Name != "bao.netsy" || got.Spec == nil || len(got.Spec.Entries) != 1 {
				t.Fatalf("unexpected request: %s", request)
			}
			entry := got.Spec.Entries[0]
			if entry.Key != "token" || entry.Operation != "create" || entry.EncryptedValue == nil {
				t.Fatalf("unexpected entry: %+v", entry)
			}
			plaintext := decryptWriteValueForTest(t, key, entry.EncryptedValue.Ciphertext, AssociatedData(WriteOptions{ClusterID: "cluster", Namespace: "system", KeyspaceName: "bao.netsy", Key: "token"}))
			defer clear(plaintext)
			if !bytes.Equal(plaintext, []byte(payload)) {
				t.Fatal("encrypted write did not preserve the generated value")
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(calls), "replace ") != 1 {
				t.Fatalf("unexpected write count: %s", calls)
			}
		})
	}
}
