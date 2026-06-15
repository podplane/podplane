// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

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
