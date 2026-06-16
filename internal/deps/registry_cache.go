// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import "path/filepath"

// RegistryCacheDir returns the shared local registry cache directory.
func (m *Manager) RegistryCacheDir() string {
	if filepath.Base(filepath.Clean(m.depsCacheDir)) != "deps" {
		return filepath.Join(m.depsCacheDir, "registry")
	}
	return filepath.Join(filepath.Dir(m.depsCacheDir), "registry")
}
