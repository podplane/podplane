// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

const localSSHKeyName = "id_ed25519"

// SSHPublicKey returns the Podplane-managed public key for the VM's
// authorized_keys file, generating the key pair if necessary.
func SSHPublicKey(dataDir string) (string, error) {
	privatePath, err := SSHPrivateKeyPath(dataDir)
	if err != nil {
		return "", err
	}
	publicPath := privatePath + ".pub"

	keyData, err := os.ReadFile(publicPath)
	if err != nil {
		return "", fmt.Errorf("read local SSH public key %s: %w", publicPath, err)
	}
	return string(bytes.TrimSpace(keyData)), nil
}

// SSHPrivateKeyPath returns the private key path for Podplane-managed local VM
// SSH access, generating the key pair if necessary.
func SSHPrivateKeyPath(dataDir string) (string, error) {
	privatePath := filepath.Join(dataDir, "ssh", localSSHKeyName)
	publicPath := privatePath + ".pub"

	if _, err := os.Stat(privatePath); err == nil {
		return privatePath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check local SSH private key %s: %w", privatePath, err)
	}
	if _, err := generateSSHKeyPair(privatePath, publicPath); err != nil {
		return "", err
	}
	return privatePath, nil
}

// generateSSHKeyPair writes a new ed25519 SSH key pair and returns the
// public key in authorized_keys format.
func generateSSHKeyPair(privatePath, publicPath string) (string, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate local SSH key: %w", err)
	}
	sshPrivateKey, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return "", fmt.Errorf("marshal local SSH private key: %w", err)
	}
	sshPublicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return "", fmt.Errorf("marshal local SSH public key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(privatePath), 0700); err != nil {
		return "", fmt.Errorf("create local SSH key directory: %w", err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(sshPrivateKey), 0600); err != nil {
		return "", fmt.Errorf("write local SSH private key %s: %w", privatePath, err)
	}
	publicKey := string(bytes.TrimSpace(ssh.MarshalAuthorizedKey(sshPublicKey)))
	if err := os.WriteFile(publicPath, []byte(publicKey+"\n"), 0644); err != nil {
		return "", fmt.Errorf("write local SSH public key %s: %w", publicPath, err)
	}
	return publicKey, nil
}
