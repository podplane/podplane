// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"errors"
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/filecache"
)

// Sentinel errors returned by Verify so callers can distinguish "user just
// hasn't downloaded yet" from "the cache is corrupted" from real errors.
var (
	ErrNotCached  = errors.New("manifest not cached")
	ErrIncomplete = errors.New("deps cache incomplete or corrupt")
)

// Verify checks that the cached manifests exist and that every dependency
// (including the OS image), vmconfig image and component image selected by
// arch and providers is present in the cache with the expected digest. Items
// are checked whether or not the cached manifests mark them cached, because a
// download for other providers leaves these providers' items uncached. On
// success it returns the parsed vmconfig manifest so callers can use it without
// re-reading from disk.
//
// Returns ErrNotCached if no cached manifest exists.
// Returns ErrIncomplete (wrapped) if any artifact is missing or has the
// wrong digest.
func (m *Manager) Verify(kind, arch string, providers []string) (*Manifest, error) {
	manifest, _, err := m.ReadCachedManifest(kind, arch)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, ErrNotCached
	}

	for _, it := range manifest.DownloadItems(ItemFilter{Providers: providers}) {
		algo, hex, err := it.Dep.ParseDigest()
		if err != nil {
			return nil, fmt.Errorf("%w: invalid digest for %s: %v", ErrIncomplete, it.Name, err)
		}
		path := m.VMConfigArtifactCachePath(it.Name, it.Dep)
		exists, err := filecache.Exists(path, algo, hex)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to check %s: %v", ErrIncomplete, it.Name, err)
		}
		if !exists {
			return nil, fmt.Errorf("%w: missing or corrupt: %s", ErrIncomplete, path)
		}
	}
	for _, image := range manifest.VMConfig.Images {
		cached, err := componentImageCached(m.RegistryCacheDir(), vmconfigComponentImage(image))
		if err != nil {
			return nil, fmt.Errorf("%w: failed to check image %s: %v", ErrIncomplete, image.Image, err)
		}
		if !cached {
			return nil, fmt.Errorf("%w: missing or corrupt image: %s", ErrIncomplete, image.Image)
		}
	}

	// Check each component image these providers need, because an earlier
	// download without them (for example `podplane deps download` with no
	// --providers) skips their provider-specific images.
	components, err := m.CachedComponentsManifest()
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: components manifest not cached", ErrIncomplete)
	}
	if err != nil {
		return nil, err
	}
	for _, index := range components.DownloadImageIndexes(ComponentImageFilter{Archs: []string{arch}, Providers: providers}) {
		image := components.Components.Images[index]
		cached, err := componentImageCached(m.RegistryCacheDir(), image)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to check image %s: %v", ErrIncomplete, image.Image, err)
		}
		if !cached {
			return nil, fmt.Errorf("%w: missing or corrupt image: %s", ErrIncomplete, image.Image)
		}
	}

	return manifest, nil
}
