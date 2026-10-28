// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package keychain_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/99designs/keyring"
	"github.com/ebitengine/purego"

	"github.com/podplane/podplane/internal/keychain"
)

// testService opts into real Keychain tests with disposable, uniquely named
// credentials. Disable dialogs so denied access fails instead of hanging CI.
func testService(t *testing.T) string {
	t.Helper()
	if os.Getenv("PODPLANE_TEST_KEYCHAIN") != "1" {
		t.Skip("set PODPLANE_TEST_KEYCHAIN=1 to test the macOS Keychain")
	}
	handle, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = purego.Dlclose(handle) })
	var getInteraction func(*bool) int32
	var setInteraction func(bool) int32
	purego.RegisterLibFunc(&getInteraction, handle, "SecKeychainGetUserInteractionAllowed")
	purego.RegisterLibFunc(&setInteraction, handle, "SecKeychainSetUserInteractionAllowed")
	var previous bool
	if status := getInteraction(&previous); status != 0 {
		t.Fatalf("get interaction: %d", status)
	}
	if status := setInteraction(false); status != 0 {
		t.Fatalf("disable interaction: %d", status)
	}
	t.Cleanup(func() {
		if status := setInteraction(previous); status != 0 {
			t.Errorf("restore interaction: %d", status)
		}
	})
	return fmt.Sprintf("dev.podplane.keychain-test.%d.%d", os.Getpid(), time.Now().UnixNano())
}

// seedCredential grants just this test executable access to a disposable entry,
// so the lifecycle test can read secrets without approving a Keychain dialog.
func seedCredential(t *testing.T, service, key string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/usr/bin/security", "add-generic-password", "-s", service, "-a", key, "-w", "fixture", "-T", executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create authorized fixture: %v: %s", err, output)
	}
}

// TestStore verifies real native create/read/update/delete operations, service
// isolation, binary data beyond the security CLI's limit, and missing items.
func TestStore(t *testing.T) {
	service := testService(t)
	s, err := keychain.New(service)
	if err != nil {
		t.Fatal(err)
	}
	key := "credential-é-🔑"
	t.Cleanup(func() {
		if err := s.Remove(key); err != nil && err != keyring.ErrKeyNotFound {
			t.Errorf("cleanup: %v", err)
		}
	})
	if _, err := s.Get(key); err != keyring.ErrKeyNotFound {
		t.Fatalf("missing Get: %v", err)
	}
	if err := s.Remove(key); err != keyring.ErrKeyNotFound {
		t.Fatalf("missing Remove: %v", err)
	}
	if err := s.Set(keyring.Item{Key: key, Data: []byte("untrusted"), Label: "Test credential"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Newly created entries require approval, like the previous backend. With
	// dialogs disabled, that must be an access error rather than a missing key.
	if _, err := s.Get(key); err == nil || err == keyring.ErrKeyNotFound {
		t.Fatalf("unapproved read should report an access error: %v", err)
	}
	if err := s.Remove(key); err != nil {
		t.Fatal(err)
	}
	seedCredential(t, service, key)
	data := make([]byte, 16385)
	for i := range data {
		data[i] = byte(i)
	}
	for _, value := range [][]byte{data, []byte("updated\x00secret"), {}} {
		want := keyring.Item{Key: key, Data: value, Label: "Label é 🔑", Description: "Test credential"}
		if err := s.Set(want); err != nil {
			t.Fatalf("Set (%d bytes): %v", len(value), err)
		}
		// Reopening and collecting catches accidentally retained Go buffers and
		// ensures reads are from Keychain rather than an in-memory copy.
		runtime.GC()
		reopened, err := keychain.New(service)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reopened.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if got.Key != want.Key || got.Label != want.Label || got.Description != want.Description || !bytes.Equal(got.Data, want.Data) {
			t.Fatalf("round-trip mismatch for %d-byte credential: got %d bytes, key=%q label=%q description=%q", len(value), len(got.Data), got.Key, got.Label, got.Description)
		}
	}
	other, err := keychain.New(service + ".other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Get(key); err != keyring.ErrKeyNotFound {
		t.Fatalf("other service Get: %v", err)
	}
	if _, err := s.Get("\xff"); err == nil || err == keyring.ErrKeyNotFound {
		t.Fatalf("invalid account should fail, not look absent: %v", err)
	}
	if err := s.Remove(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(key); err != keyring.ErrKeyNotFound {
		t.Fatalf("deleted Get: %v", err)
	}
}
