// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v6"
	"github.com/podplane/podplane/internal/filecache"
)

// VerifyOffline verifies the complete download selection without network access,
// including addon/template images, chart layers, seed snapshots and Git source.
// It does not treat cached flags as evidence of file integrity.
func (m *Manager) VerifyOffline(kind, arch string, providers, addons []string) (*Manifest, error) {
	manifest, err := m.Verify(kind, arch, providers)
	if err != nil {
		return nil, err
	}
	components, err := m.CachedComponentsManifest()
	if err != nil {
		return nil, err
	}
	for _, i := range components.DownloadImageIndexes(ComponentImageFilter{Archs: []string{arch}, Providers: providers, Addons: addons}) {
		image := components.Components.Images[i]
		if !image.Addon {
			continue // Verify already checked the base component images.
		}
		if ok, err := componentImageCached(m.RegistryCacheDir(), image); err != nil || !ok {
			return nil, fmt.Errorf("%w: component image %s: %v", ErrIncomplete, image.Image, err)
		}
	}
	if source := components.Components.Source; source != nil {
		if err := validateComponentsSource(source); err != nil {
			return nil, err
		}
		gitPath, err := m.ComponentsGitCachePath(source.URL)
		if err != nil {
			return nil, err
		}
		// Downloads produce bare mirrors. Git's worktree metadata can redirect
		// PlainOpen to another filesystem root, outside an imported snapshot.
		for _, name := range []string{".git", "commondir"} {
			if _, err := os.Lstat(filepath.Join(gitPath, name)); err == nil {
				return nil, fmt.Errorf("%w: components Git source must be a self-contained bare mirror (contains %s)", ErrIncomplete, name)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		repo, err := git.PlainOpen(gitPath)
		if err != nil {
			return nil, fmt.Errorf("%w: components Git source: %v", ErrIncomplete, err)
		}
		defer func() { _ = repo.Close() }()
		if err := verifyCachedComponentsGitRef(repo, source); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrIncomplete, err)
		}
	}
	templates, _, err := m.ReadCachedTemplatesManifest()
	if err != nil || templates == nil {
		return nil, fmt.Errorf("%w: templates manifest: %v", ErrIncomplete, err)
	}
	for _, i := range templates.DownloadImageIndexes([]string{arch}) {
		image := templateComponentImage(templates.Templates.Images[i])
		if ok, err := componentImageCached(m.RegistryCacheDir(), image); err != nil || !ok {
			return nil, fmt.Errorf("%w: template image %s: %v", ErrIncomplete, image.Image, err)
		}
	}
	for _, chart := range templates.Templates.Charts {
		algo, digest, ok := strings.Cut(chart.ChartLayerDigest, ":")
		if !chart.Cached || !ok || algo != "sha256" {
			return nil, fmt.Errorf("%w: template %s is not cached or has no chart digest", ErrIncomplete, chart.Name)
		}
		filename := blobPath(filepath.Join(m.TemplatesChartsCacheDir(), filepath.FromSlash(templateChartRepo(chart))), chart.ChartLayerDigest)
		if ok, err := filecache.Exists(filename, algo, digest); err != nil || !ok {
			return nil, fmt.Errorf("%w: template chart %s: %v", ErrIncomplete, chart.Name, err)
		}
	}
	seeds, _, err := m.ReadCachedSeedsManifest()
	if err != nil || seeds == nil {
		return nil, fmt.Errorf("%w: seeds manifest: %v", ErrIncomplete, err)
	}
	if seeds.Seeds.Components != "" && seeds.Seeds.Components != components.Components.Version {
		return nil, fmt.Errorf("%w: seeds require components %s, cache contains %s", ErrIncomplete, seeds.Seeds.Components, components.Components.Version)
	}
	for name, snapshot := range seeds.Seeds.Snapshots {
		algo, digest, err := parseSeedSnapshotDigest(name, snapshot)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrIncomplete, err)
		}
		if ok, err := filecache.Exists(m.seedSnapshotCachePath(seeds, name, snapshot), algo, digest); err != nil || !ok {
			return nil, fmt.Errorf("%w: seed snapshot %s: %v", ErrIncomplete, name, err)
		}
		if !snapshot.Cached {
			return nil, fmt.Errorf("%w: seed snapshot %s is not marked cached", ErrIncomplete, name)
		}
	}
	return manifest, nil
}
