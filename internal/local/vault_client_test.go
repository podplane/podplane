// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"net/http"
	"os"
	"testing"

	"github.com/podplane/podplane/internal/fakevault"
)

// localVaultTestKeyring stores fake Vault encryption keys in memory.
type localVaultTestKeyring map[string][]byte

// KeyringWrite stores a test keyring value.
func (k localVaultTestKeyring) KeyringWrite(key string, value []byte) error {
	k[key] = bytes.Clone(value)
	return nil
}

// KeyringRead returns a test keyring value.
func (k localVaultTestKeyring) KeyringRead(key string) ([]byte, error) {
	return bytes.Clone(k[key]), nil
}

// KeyringDelete removes a test keyring value.
func (k localVaultTestKeyring) KeyringDelete(key string) error {
	delete(k, key)
	return nil
}

// TestLocalVaultClient verifies host bootstrap and deletion use the trusted API
// over its Unix socket without a Vault token.
func TestLocalVaultClient(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := localVaultListener(path)
	if err != nil {
		t.Fatalf("localVaultListener: %v", err)
	}
	store := fakevault.NewFileStore(localVaultTestKeyring{}, t.TempDir())
	server := &http.Server{Handler: fakevault.NewTrustedHandler(store)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(path)
	})

	client := newLocalVaultClient(path)
	created, err := client.CreateSecret("dev", "secret/data/dev/workload-ca-key", map[string]string{"value": "first"})
	if err != nil || !created {
		t.Fatalf("CreateSecret = %v, %v; want true, nil", created, err)
	}
	created, err = client.CreateSecret("dev", "secret/data/dev/workload-ca-key", map[string]string{"value": "second"})
	if err != nil || created {
		t.Fatalf("second CreateSecret = %v, %v; want false, nil", created, err)
	}
	got, ok, err := client.GetSecret("dev", "secret/data/dev/workload-ca-key")
	if err != nil || !ok || got["value"] != "first" {
		t.Fatalf("GetSecret = %#v, %v, %v", got, ok, err)
	}
	if err := client.DeleteCluster("dev"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, ok, err := client.GetSecret("dev", "secret/data/dev/workload-ca-key"); err != nil || ok {
		t.Fatalf("GetSecret after delete = %v, %v; want false, nil", ok, err)
	}
}
