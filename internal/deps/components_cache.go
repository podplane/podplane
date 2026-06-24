// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ComponentsCacheDir returns the component dependency-domain cache directory.
func (m *Manager) ComponentsCacheDir() string {
	return filepath.Join(m.depsCacheDir, "components")
}

// ComponentsManifestCacheDir returns the component manifest cache directory.
func (m *Manager) ComponentsManifestCacheDir() string {
	return filepath.Join(m.ComponentsCacheDir(), "manifests")
}

// ComponentsManifestCachePath returns the path to the cached components manifest.
func (m *Manager) ComponentsManifestCachePath() string {
	return filepath.Join(m.ComponentsManifestCacheDir(), "components.json")
}

// ComponentsHistoricalManifestCachePath returns an immutable historical component manifest path.
func (m *Manager) ComponentsHistoricalManifestCachePath(raw []byte) string {
	sum := sha256.Sum256(raw)
	filename := fmt.Sprintf("components-%s-%s.json", time.Now().UTC().Format("20060102T150405Z"), hex.EncodeToString(sum[:])[:12])
	return filepath.Join(m.ComponentsManifestCacheDir(), filename)
}

// WriteCachedComponentsManifest writes historical and latest components manifest JSON bytes.
func (m *Manager) WriteCachedComponentsManifest(raw []byte) error {
	historicalPath := m.ComponentsHistoricalManifestCachePath(raw)
	if err := os.MkdirAll(filepath.Dir(historicalPath), 0755); err != nil {
		return fmt.Errorf("failed to create components cache directory: %w", err)
	}
	if err := os.WriteFile(historicalPath, raw, 0644); err != nil {
		return fmt.Errorf("failed to write historical cached components manifest: %w", err)
	}
	latestPath := m.ComponentsManifestCachePath()
	if err := os.WriteFile(latestPath, raw, 0644); err != nil {
		return fmt.Errorf("failed to write cached components manifest: %w", err)
	}
	return nil
}

// CachedComponentsVersion returns the version from the cached components manifest.
func (m *Manager) CachedComponentsVersion() (string, error) {
	manifest, err := m.CachedComponentsManifest()
	if err != nil {
		return "", err
	}
	if manifest.Components.Version == "" {
		return "", fmt.Errorf("cached components manifest is missing version")
	}
	return manifest.Components.Version, nil
}

// CachedComponentsManifest returns the parsed cached components manifest.
func (m *Manager) CachedComponentsManifest() (*ComponentsManifest, error) {
	raw, err := os.ReadFile(m.ComponentsManifestCachePath())
	if err != nil {
		return nil, fmt.Errorf("read cached components manifest: %w", err)
	}
	var manifest ComponentsManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse cached components manifest: %w", err)
	}
	return &manifest, nil
}
