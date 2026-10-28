// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !darwin

package config

import "github.com/99designs/keyring"

// openSystemKeyring uses the existing backends on other operating systems.
func openSystemKeyring(cfg keyring.Config) (credentialStore, error) {
	return keyring.Open(cfg)
}
