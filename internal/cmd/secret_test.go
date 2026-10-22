// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/secrets"
)

// TestSecretBinaryWrites verifies the secret command's create/update paths carry
// explicit binary intent for file/stdin input, including encrypted dry runs.
func TestSecretBinaryWrites(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := config.Init()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetClusterSummary(config.ClusterSummary{ID: "cluster", Secrets: clusterconfig.Secrets{
		DefaultProvider: "primary", Providers: map[string]clusterconfig.SecretsProvider{"primary": {Kind: "aws"}},
	}}, false); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"create", "update"} {
		for _, tc := range []struct {
			name, value           string
			binary, stdin, dryRun bool
			wantErr               bool
		}{
			{name: "default text", value: "café\n"},
			{name: "reject invalid text", value: "private\xff", wantErr: true},
			{name: "binary file", value: "\x00\xff\xc3\n", binary: true},
			{name: "binary stdin dry run", value: "\x00\xff\xc3\n", binary: true, stdin: true, dryRun: true},
			{name: "text-shaped binary", value: "YWJjZA==", binary: true},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				write := func(name string, data []byte) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(dir, name), data, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				key, err := ecdh.X25519().GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				public, err := json.Marshal(secrets.PublicKey{Spec: secrets.PublicKeySpec{KeyID: "latest", Algorithm: secrets.Algorithm, PublicKey: base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())}})
				if err != nil {
					t.Fatal(err)
				}
				write("public", public)
				write("input", []byte(tc.value))
				write("kubectl", []byte(`#!/bin/sh
case "$*" in
  *"config view"*) echo '{"current-context":"chosen","contexts":[{"name":"chosen","context":{"cluster":"podplane-cluster"}}]}';;
  *publickeys/latest*) cat "$STATE/public";;
  *) cat > "$STATE/request"; echo '{}';;
esac
`))
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("STATE", dir)
				output, err := os.Create(filepath.Join(dir, "output"))
				if err != nil {
					t.Fatal(err)
				}
				oldOut := os.Stdout
				os.Stdout = output
				t.Cleanup(func() { os.Stdout = oldOut; _ = output.Close() })
				args := []string{operation, "key", "--for", "app"}
				if tc.binary {
					args = append(args, "--binary")
				}
				if tc.stdin {
					input, err := os.Open(filepath.Join(dir, "input"))
					if err != nil {
						t.Fatal(err)
					}
					oldIn := os.Stdin
					os.Stdin = input
					t.Cleanup(func() { os.Stdin = oldIn; _ = input.Close() })
					args = append(args, "--stdin")
				} else {
					args = append(args, "--file", filepath.Join(dir, "input"))
				}
				if tc.dryRun {
					args = append(args, "--dry-run", "client", "-o", "json")
				}
				cmd := newSecretCmd(c)
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(args)
				err = cmd.Execute()
				if tc.wantErr {
					if err == nil || err.Error() != "secret value must be valid UTF-8 text" {
						t.Fatalf("invalid text error = %v", err)
					}
					if _, err := os.Stat(filepath.Join(dir, "request")); !os.IsNotExist(err) {
						t.Fatal("invalid text was written")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				path := "request"
				if tc.dryRun {
					if _, err := os.Stat(filepath.Join(dir, "request")); !os.IsNotExist(err) {
						t.Fatal("dry run wrote to the cluster")
					}
					path = "output"
				}
				data, err := os.ReadFile(filepath.Join(dir, path))
				if err != nil {
					t.Fatal(err)
				}
				var request secrets.SecretProviderKeyspace
				if err := json.Unmarshal(data, &request); err != nil {
					t.Fatal(err)
				}
				if request.Spec == nil || len(request.Spec.Entries) != 1 {
					t.Fatal("missing encrypted entry")
				}
				entry := request.Spec.Entries[0]
				if entry.Operation != operation || entry.Binary != tc.binary || entry.EncryptedValue == nil {
					t.Fatal("command lost operation, binary intent, or encryption")
				}
			})
		}
	}
}

// TestReadSecretValueFromFile verifies --file reads the exact file bytes.
func TestReadSecretValueFromFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secret-value")
	if err := os.WriteFile(path, []byte("secret\nvalue"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	value, err := readSecretValue("create", "api-key", false, path)
	if err != nil {
		t.Fatalf("readSecretValue() error = %v", err)
	}
	if got, want := string(value), "secret\nvalue"; got != want {
		t.Fatalf("readSecretValue() = %q, want %q", got, want)
	}
}

// TestReadSecretValueRejectsFileAndStdin verifies --file and --stdin cannot both be set.
func TestReadSecretValueRejectsFileAndStdin(t *testing.T) {
	t.Parallel()
	_, err := readSecretValue("create", "api-key", true, "secret.txt")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("readSecretValue() error = %v, want mutual exclusion error", err)
	}
}
