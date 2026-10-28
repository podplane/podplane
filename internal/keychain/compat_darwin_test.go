// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

//go:build darwin && cgo

package keychain_test

import (
	"bytes"
	"testing"

	"github.com/99designs/keyring"

	"github.com/podplane/podplane/internal/keychain"
)

// TestLegacyCompatibility verifies both directions without migrating entries,
// including an update to an entry created by the previous cgo backend.
func TestLegacyCompatibility(t *testing.T) {
	service := testService(t)
	legacy, err := keyring.Open(keyring.Config{
		ServiceName: service, AllowedBackends: []keyring.BackendType{keyring.KeychainBackend},
		KeychainTrustApplication: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	native, err := keychain.New(service)
	if err != nil {
		t.Fatal(err)
	}
	key := "compatibility"
	t.Cleanup(func() {
		if err := native.Remove(key); err != nil && err != keyring.ErrKeyNotFound {
			t.Errorf("cleanup: %v", err)
		}
	})
	want := keyring.Item{Key: key, Data: []byte("old\x00binary\xff"), Label: "Old label", Description: "Old description"}
	if err := legacy.Set(want); err != nil {
		t.Fatal(err)
	}
	got, err := native.Get(key)
	if err != nil || !bytes.Equal(got.Data, want.Data) || got.Label != want.Label || got.Description != want.Description {
		t.Fatalf("native read of legacy entry: err=%v, data match=%t", err, bytes.Equal(got.Data, want.Data))
	}
	want.Data = []byte("new\x00binary\xfe")
	if err := native.Set(want); err != nil {
		t.Fatal(err)
	}
	got, err = legacy.Get(key)
	if err != nil || !bytes.Equal(got.Data, want.Data) || got.Label != want.Label || got.Description != want.Description {
		t.Fatalf("legacy read of updated entry: err=%v, data match=%t", err, bytes.Equal(got.Data, want.Data))
	}
	if err := legacy.Remove(key); err != nil {
		t.Fatal(err)
	}
	if err := native.Set(want); err != nil {
		t.Fatal(err)
	}
	// The legacy backend can locate and delete entries created by the native
	// backend without changing their deliberately untrusted read ACL.
	if err := legacy.Remove(key); err != nil {
		t.Fatalf("legacy delete of new native entry: %v", err)
	}
	if _, err := native.Get(key); err != keyring.ErrKeyNotFound {
		t.Fatalf("native Get after legacy delete: %v", err)
	}
}
