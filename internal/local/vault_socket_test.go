// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLocalVaultListenerRestrictsSocket verifies the trusted API socket is
// accessible only to its owner.
func TestLocalVaultListenerRestrictsSocket(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := localVaultListener(path)
	if err != nil {
		t.Fatalf("localVaultListener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("socket mode = %o, want %o", got, want)
	}
}

// TestLocalVaultListenerReplacesStaleSocket verifies an unserved socket left by
// a terminated process does not block the next local server start.
func TestLocalVaultListenerReplacesStaleSocket(t *testing.T) {
	path := shortSocketPath(t)
	stale, err := localVaultListener(path)
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale socket: %v", err)
	}
	listener, err := localVaultListener(path)
	if err != nil {
		t.Fatalf("replace stale socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
}

// TestLocalVaultListenerRefusesActiveSocket verifies concurrent local servers
// cannot silently replace one another's trusted API socket.
func TestLocalVaultListenerRefusesActiveSocket(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := localVaultListener(path)
	if err != nil {
		t.Fatalf("localVaultListener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if _, err := localVaultListener(path); err == nil {
		t.Fatal("localVaultListener replaced an active socket")
	}
}

// TestLocalVaultListenerRefusesNonSocket verifies startup never removes an
// unrelated file at the configured socket path.
func TestLocalVaultListenerRefusesNonSocket(t *testing.T) {
	path := shortSocketPath(t)
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if _, err := localVaultListener(path); err == nil {
		t.Fatal("localVaultListener accepted a non-socket path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("sentinel = %q, want keep", data)
	}
}

// shortSocketPath returns a temporary path below Unix socket length limits.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "podplane-")
	if err != nil {
		t.Fatalf("create temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "vault.sock")
}
