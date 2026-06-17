// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import "testing"

type testKeyring struct {
	items map[string][]byte
}

// KeyringWrite stores a test keyring item in memory.
func (k *testKeyring) KeyringWrite(key string, value []byte) error {
	if k.items == nil {
		k.items = map[string][]byte{}
	}
	k.items[key] = append([]byte{}, value...)
	return nil
}

// KeyringRead returns a test keyring item from memory.
func (k *testKeyring) KeyringRead(key string) ([]byte, error) {
	value := k.items[key]
	return append([]byte{}, value...), nil
}

// KeyringDelete removes a test keyring item from memory.
func (k *testKeyring) KeyringDelete(key string) error {
	delete(k.items, key)
	return nil
}

func TestKeyringStoreSetGetListDeleteSecret(t *testing.T) {
	store := NewKeyringStore(&testKeyring{})
	values := map[string]string{"username": "app", "password": "secret"}
	if err := store.SetSecret("dev", "/v1/secret/data/apps/app/db", values); err != nil {
		t.Fatalf("SetSecret: %v", err)
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
