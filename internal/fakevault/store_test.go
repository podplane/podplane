// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testKeyring struct {
	items  map[string][]byte
	reads  map[string]int
	writes map[string]int
}

// KeyringWrite stores a test keyring item in memory.
func (k *testKeyring) KeyringWrite(key string, value []byte) error {
	if k.items == nil {
		k.items = map[string][]byte{}
	}
	if k.writes == nil {
		k.writes = map[string]int{}
	}
	k.items[key] = append([]byte{}, value...)
	k.writes[key]++
	return nil
}

// KeyringRead returns a test keyring item from memory.
func (k *testKeyring) KeyringRead(key string) ([]byte, error) {
	if k.reads == nil {
		k.reads = map[string]int{}
	}
	k.reads[key]++
	value := k.items[key]
	return append([]byte{}, value...), nil
}

// TestFileStoreSetGetListDeleteSecret verifies encrypted file-backed secret
// lifecycle operations.
func TestFileStoreSetGetListDeleteSecret(t *testing.T) {
	keyring := &testKeyring{}
	store := NewFileStore(keyring, t.TempDir())
	values := map[string]string{"username": "app", "password": "secret"}
	if err := store.SetSecret("dev", "/v1/secret/data/apps/app/db", values); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	secretPath := filepath.Join(store.root, "dev", "fakevault", "secret", "data", "apps", "app", "db.json")
	data, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read secret file: %v", err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "app") {
		t.Fatalf("secret file contains plaintext: %s", data)
	}

	got, ok, err := store.GetSecret("dev", "secret/data/apps/app/db")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if !ok {
		t.Fatalf("GetSecret ok = false")
	}
	if got["username"] != "app" || got["password"] != "secret" {
		t.Fatalf("GetSecret = %#v", got)
	}

	secrets, err := store.ListSecrets("dev")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(secrets) != 1 || secrets[0].Path != "secret/data/apps/app/db" {
		t.Fatalf("ListSecrets = %#v", secrets)
	}
	if got := strings.Join(secrets[0].Keys, ","); got != "password,username" {
		t.Fatalf("ListSecrets keys = %q", got)
	}

	if err := store.DeleteSecret("dev", "secret/data/apps/app/db"); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	_, ok, err = store.GetSecret("dev", "secret/data/apps/app/db")
	if err != nil {
		t.Fatalf("GetSecret after delete: %v", err)
	}
	if ok {
		t.Fatalf("GetSecret after delete ok = true")
	}
}

// TestFileStoreUsesOneCachedVaultKeyPerCluster verifies file-backed secrets use
// one lazily cached keychain item per local cluster.
func TestFileStoreUsesOneCachedVaultKeyPerCluster(t *testing.T) {
	keyring := &testKeyring{}
	store := NewFileStore(keyring, t.TempDir())
	if err := store.SetSecret("dev", "secret/data/apps/app/db", map[string]string{"password": "secret"}); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if _, _, err := store.GetSecret("dev", "secret/data/apps/app/db"); err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if err := store.SetSecret("other", "secret/data/apps/app/db", map[string]string{"password": "secret"}); err != nil {
		t.Fatalf("SetSecret other: %v", err)
	}

	devKey := vaultKeyName("dev")
	if keyring.reads[devKey] != 1 {
		t.Fatalf("dev key reads = %d, want 1", keyring.reads[devKey])
	}
	if keyring.writes[devKey] != 1 {
		t.Fatalf("dev key writes = %d, want 1", keyring.writes[devKey])
	}
	if _, ok := keyring.items["dev.podplane.fakevault.dev:_index"]; ok {
		t.Fatalf("legacy fakevault index was written")
	}
	if _, ok := keyring.items["dev.podplane.fakevault.dev:secret/data/apps/app/db"]; ok {
		t.Fatalf("legacy per-secret keychain item was written")
	}
	if _, ok := keyring.items[vaultKeyName("other")]; !ok {
		t.Fatalf("other cluster vault key was not written")
	}
}

// TestFileStoreArchiveRestoreSecret verifies recoverable fakevault deletes are
// represented as file metadata, not keychain writes.
func TestFileStoreArchiveRestoreSecret(t *testing.T) {
	store := NewFileStore(&testKeyring{}, t.TempDir())
	if err := store.SetSecret("dev", "secret/data/apps/app/db", map[string]string{"password": "secret"}); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if err := store.ArchiveSecret("dev", "secret/data/apps/app/db"); err != nil {
		t.Fatalf("ArchiveSecret: %v", err)
	}
	if _, ok, err := store.GetSecret("dev", "secret/data/apps/app/db"); err != nil || ok {
		t.Fatalf("GetSecret archived ok=%v err=%v", ok, err)
	}
	secrets, err := store.ListSecrets("dev")
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(secrets) != 1 || !secrets[0].Archived {
		t.Fatalf("ListSecrets archived = %#v", secrets)
	}
	if err := store.RestoreSecret("dev", "secret/data/apps/app/db"); err != nil {
		t.Fatalf("RestoreSecret: %v", err)
	}
	if _, ok, err := store.GetSecret("dev", "secret/data/apps/app/db"); err != nil || !ok {
		t.Fatalf("GetSecret restored ok=%v err=%v", ok, err)
	}
}
