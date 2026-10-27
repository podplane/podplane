// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"fmt"
	"path/filepath"
)

// Manager handles fetching and caching manifests and the artifacts/images
// they reference.
type Manager struct {
	baseURL      string
	depsCacheDir string
}

// NewManager creates a new deps manager.
//
// baseURL is the base URL used to fetch manifest files, e.g.
// "https://deps.podplane.dev". depsCacheDir is the local deps cache root.
func NewManager(baseURL, depsCacheDir string) *Manager {
	return &Manager{
		baseURL:      baseURL,
		depsCacheDir: depsCacheDir,
	}
}

// BaseURL returns the source URL used to fetch dependency manifests.
func (m *Manager) BaseURL() string { return m.baseURL }

// CacheDir returns the dependency cache root.
func (m *Manager) CacheDir() string { return m.depsCacheDir }

// validateCachePath rejects metadata-derived paths outside their cache domain.
// It checks lexical containment; it does not resolve filesystem symlinks.
func validateCachePath(root, filename string) error {
	rel, err := filepath.Rel(root, filename)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return fmt.Errorf("cache path %q is outside %q", filename, root)
	}
	return nil
}
