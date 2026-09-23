// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/99designs/keyring"
)

const (
	// KeyringPassEnv is the environment variable used to force and unlock the
	// file-based keyring backend.
	KeyringPassEnv = "PODPLANE_KEYRING_PASS"

	// LocalKeyringPass is the fixed file-keyring password used by local kubectl
	// exec auth so kubectl can read local dev credentials non-interactively.
	LocalKeyringPass = "podplane-local"
)

// keyringPasswordFunc is a custom password function for file-based keyring
// that uses the PODPLANE_KEYRING_PASS environment variable.
func keyringPasswordFunc(prompt string) (string, error) {
	password := os.Getenv(KeyringPassEnv)
	if password == "" {
		return "", fmt.Errorf("%s environment variable must be set for file-based keyring", KeyringPassEnv)
	}
	return password, nil
}

// InitWithLocalKeyring initializes a Config that uses the same file-based
// keyring backend as local kubectl exec auth. The returned restore function
// resets the process environment to its previous state.
func InitWithLocalKeyring() (*Config, func(), error) {
	previous, hadPrevious := os.LookupEnv(KeyringPassEnv)
	if err := os.Setenv(KeyringPassEnv, LocalKeyringPass); err != nil {
		return nil, func() {}, fmt.Errorf("set local keyring password: %w", err)
	}
	restore := func() {
		if hadPrevious {
			_ = os.Setenv(KeyringPassEnv, previous)
			return
		}
		_ = os.Unsetenv(KeyringPassEnv)
	}
	c, err := Init()
	if err != nil {
		restore()
		return nil, func() {}, err
	}
	return c, restore, nil
}

// initKeyring opens the configured keyring once for this Config.
func (c *Config) initKeyring() error {
	c.keyringMu.Lock()
	defer c.keyringMu.Unlock()
	if c.keyring != nil {
		return nil
	}
	keyringDir := filepath.Join(c.ConfigDirectory(), "keyring")

	// Ensure file-based keyring directory exists in case we need it
	if err := os.MkdirAll(keyringDir, 0700); err != nil {
		return fmt.Errorf("unable to create keyring directory %s: %w", keyringDir, err)
	}

	keyringConfig := keyring.Config{
		ServiceName:      "podplane",
		FileDir:          keyringDir,
		FilePasswordFunc: keyringPasswordFunc,
	}

	// If PODPLANE_KEYRING_PASS is set, force file-based backend
	// This allows users to bypass OS keychain on any platform
	if os.Getenv(KeyringPassEnv) != "" {
		keyringConfig.AllowedBackends = []keyring.BackendType{keyring.FileBackend}
	}

	// note: OS may prompt the user for permission
	ring, err := keyring.Open(keyringConfig)
	if err != nil {
		return fmt.Errorf("open keyring: %w", err)
	}
	c.keyring = ring
	return nil
}

// KeyringWrite stores a value in the configured OS or file-backed keyring.
func (c *Config) KeyringWrite(key string, value []byte) error {
	// Ensure keyring is initialised; OS may prompt the user for permission.
	err := c.initKeyring()
	if err != nil {
		return err
	}
	// store the token in the keyring
	err = c.keyring.Set(keyring.Item{
		Key:   key,
		Label: key,
		Data:  value,
	})
	if err != nil {
		return fmt.Errorf("write keyring item %q: %w", key, err)
	}
	return nil
}

// KeyringRead returns a value from the configured OS or file-backed keyring.
// It returns nil without an error when the item does not exist.
func (c *Config) KeyringRead(key string) ([]byte, error) {
	// Ensure keyring is initialised; OS may prompt the user for permission.
	err := c.initKeyring()
	if err != nil {
		return nil, err
	}
	item, err := c.keyring.Get(key)
	if err != nil {
		if err == keyring.ErrKeyNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("read keyring item %q: %w", key, err)
	}
	return item.Data, nil
}

// KeyringDelete removes a value from the configured OS or file-backed keyring.
func (c *Config) KeyringDelete(key string) error {
	// Ensure keyring is initialised; OS may prompt the user for permission.
	err := c.initKeyring()
	if err != nil {
		return err
	}
	// delete the token from the keyring
	err = c.keyring.Remove(key)
	if err != nil {
		if err == keyring.ErrKeyNotFound || os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("delete keyring item %q: %w", key, err)
	}
	return nil
}
