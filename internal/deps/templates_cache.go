// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const templatesManifestPath = "manifests/templates.json"

// TemplatesCacheDir returns the templates dependency-domain cache directory.
func (m *Manager) TemplatesCacheDir() string {
	return filepath.Join(m.depsCacheDir, "templates")
}

// TemplatesManifestCacheDir returns the templates manifest cache directory.
func (m *Manager) TemplatesManifestCacheDir() string {
	return filepath.Join(m.TemplatesCacheDir(), "manifests")
}

// TemplatesChartsCacheDir returns the cached template chart data directory.
func (m *Manager) TemplatesChartsCacheDir() string {
	return filepath.Join(m.TemplatesCacheDir(), "charts")
}

// TemplatesManifestCachePath returns the latest cached templates manifest path.
func (m *Manager) TemplatesManifestCachePath() string {
	return filepath.Join(m.TemplatesManifestCacheDir(), "templates.json")
}

// WriteCachedTemplatesManifest writes historical and latest templates manifests.
func (m *Manager) WriteCachedTemplatesManifest(raw []byte) error {
	historicalPath := filepath.Join(m.TemplatesManifestCacheDir(), historicalManifestFilename("templates", raw))
	if err := os.MkdirAll(filepath.Dir(historicalPath), 0o755); err != nil {
		return fmt.Errorf("failed to create templates manifest cache directory: %w", err)
	}
	if err := os.WriteFile(historicalPath, raw, 0o644); err != nil {
		return fmt.Errorf("failed to write historical templates manifest: %w", err)
	}
	if err := os.WriteFile(m.TemplatesManifestCachePath(), raw, 0o644); err != nil {
		return fmt.Errorf("failed to write templates manifest: %w", err)
	}
	return nil
}

// ReadCachedTemplatesManifest reads the latest cached templates manifest.
func (m *Manager) ReadCachedTemplatesManifest() (*TemplatesManifest, []byte, error) {
	raw, err := os.ReadFile(m.TemplatesManifestCachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to read cached templates manifest: %w", err)
	}
	var manifest TemplatesManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, raw, fmt.Errorf("failed to parse cached templates manifest: %w", err)
	}
	for _, chart := range manifest.Templates.Charts {
		repo := filepath.Join(m.TemplatesChartsCacheDir(), filepath.FromSlash(templateChartRepo(chart)))
		if err := validateCachePath(m.TemplatesChartsCacheDir(), repo); err != nil {
			return nil, raw, err
		}
		if chart.ChartLayerDigest != "" {
			if err := validateBlobDigest(chart.ChartLayerDigest); err != nil {
				return nil, raw, err
			}
		}
	}
	return &manifest, raw, nil
}

// CachedTemplateChart returns the cached template chart metadata and local
// Helm chart archive path for name.
func (m *Manager) CachedTemplateChart(name string) (TemplateChart, string, error) {
	manifest, _, err := m.ReadCachedTemplatesManifest()
	if err != nil {
		return TemplateChart{}, "", err
	}
	if manifest == nil {
		return TemplateChart{}, "", fmt.Errorf("templates manifest is not cached; run `podplane deps download`")
	}
	for _, chart := range manifest.Templates.Charts {
		if chart.Name != name {
			continue
		}
		if !chart.Cached || chart.ChartLayerDigest == "" {
			return TemplateChart{}, "", fmt.Errorf("template %q is not cached; run `podplane deps download`", name)
		}
		repo := templateChartRepo(chart)
		if repo == "" {
			return TemplateChart{}, "", fmt.Errorf("cached template %q has no repository path", name)
		}
		path := blobPath(filepath.Join(m.TemplatesChartsCacheDir(), zotRootDirectory, filepath.FromSlash(repo)), chart.ChartLayerDigest)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return TemplateChart{}, "", fmt.Errorf("template %q chart layer is missing from cache; run `podplane deps download`", name)
			}
			return TemplateChart{}, "", err
		}
		return chart, path, nil
	}
	return TemplateChart{}, "", fmt.Errorf("template %q was not found in cached templates manifest", name)
}

// CachedTemplateChartPath returns the cached Helm chart archive path for name.
func (m *Manager) CachedTemplateChartPath(name string) (string, error) {
	_, path, err := m.CachedTemplateChart(name)
	return path, err
}

// templateChartRepo returns the local chart repository path for a template.
func templateChartRepo(chart TemplateChart) string {
	if chart.URL == "" {
		return "podplane.local/templates/" + chart.Name
	}
	repo, _, _ := splitImageRef(strings.TrimPrefix(chart.URL, "oci://"))
	return repo
}
