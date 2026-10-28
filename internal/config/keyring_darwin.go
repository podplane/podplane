// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"github.com/99designs/keyring"

	"github.com/podplane/podplane/internal/keychain"
)

// openSystemKeyring uses the macOS Keychain even in builds without cgo.
func openSystemKeyring(cfg keyring.Config) (credentialStore, error) {
	return keychain.New(cfg.ServiceName)
}
