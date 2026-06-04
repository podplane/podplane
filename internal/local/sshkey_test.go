// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSSHKeyForLocalVMGeneratesPodplaneKeyWhenUserHasNoSSHKey(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	dataDir := t.TempDir()

	publicKey, err := SSHPublicKey(dataDir)
	if err != nil {
		t.Fatalf("SSHKeyForLocalVM: %v", err)
	}

	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey)); err != nil {
		t.Fatalf("generated public key is not authorized_keys format: %v", err)
	}
	privateKeyPath, err := SSHPrivateKeyPath(dataDir)
	if err != nil {
		t.Fatalf("SSHPrivateKeyPath: %v", err)
	}
	assertFileMode(t, privateKeyPath, 0600)
	assertFileMode(t, privateKeyPath+".pub", 0644)
}

func TestSSHKeyForLocalVMPrefersExistingPodplaneKey(t *testing.T) {
	dataDir := t.TempDir()
	podplaneKey := "ssh-ed25519 AAAApodplane"
	podplaneIdentityFile := filepath.Join(dataDir, "ssh", localSSHKeyName)
	if err := os.MkdirAll(filepath.Dir(podplaneIdentityFile), 0700); err != nil {
		t.Fatalf("mkdir podplane ssh dir: %v", err)
	}
	if err := os.WriteFile(podplaneIdentityFile, []byte("private"), 0600); err != nil {
		t.Fatalf("write podplane private key: %v", err)
	}
	if err := os.WriteFile(podplaneIdentityFile+".pub", []byte(podplaneKey+"\n"), 0644); err != nil {
		t.Fatalf("write podplane public key: %v", err)
	}

	publicKey, err := SSHPublicKey(dataDir)
	if err != nil {
		t.Fatalf("SSHKeyForLocalVM: %v", err)
	}
	if publicKey != podplaneKey {
		t.Fatalf("public key = %q, want %q", publicKey, podplaneKey)
	}
	privateKeyPath, err := SSHPrivateKeyPath(dataDir)
	if err != nil {
		t.Fatalf("SSHPrivateKeyPath: %v", err)
	}
	if privateKeyPath != podplaneIdentityFile {
		t.Fatalf("identity file = %q, want %q", privateKeyPath, podplaneIdentityFile)
	}
}

func TestSSHKeyForLocalVMIgnoresUserSSHKey(t *testing.T) {
	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	userKey := "ssh-ed25519 AAAAuser"
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte(userKey+"\n"), 0644); err != nil {
		t.Fatalf("write user key: %v", err)
	}
	t.Setenv("HOME", homeDir)

	dataDir := t.TempDir()
	publicKey, err := SSHPublicKey(dataDir)
	if err != nil {
		t.Fatalf("SSHKeyForLocalVM: %v", err)
	}
	if publicKey == userKey {
		t.Fatalf("public key unexpectedly used user SSH key")
	}
	privateKeyPath, err := SSHPrivateKeyPath(dataDir)
	if err != nil {
		t.Fatalf("SSHPrivateKeyPath: %v", err)
	}
	assertFileMode(t, privateKeyPath, 0600)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}
