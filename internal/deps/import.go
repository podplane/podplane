// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/internal/filecache"
)

// Import merges a verified snapshot's deps/ and registry/ trees into the cache.
// The caller must isolate the snapshot and verify it against the supplied files.
// It verifies dependency completeness without downloading or executing tools.
// Immutable files are installed first, OCI indexes next, manifests last. An
// interrupted import can be repeated without deleting unrelated cached data.
func (m *Manager) Import(ctx context.Context, directory string, files []filecache.File, kind, arch string, providers, addons []string) error {
	cache := NewManager("", filepath.Join(directory, "deps"))
	if _, err := cache.VerifyOffline(kind, arch, providers, addons); err != nil {
		return fmt.Errorf("incomplete bundle: %w", err)
	}
	src, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	for _, dir := range []string{m.depsCacheDir, m.RegistryCacheDir()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	depsRoot, err := os.OpenRoot(m.depsCacheDir)
	if err != nil {
		return err
	}
	defer func() { _ = depsRoot.Close() }()
	registryRoot, err := os.OpenRoot(m.RegistryCacheDir())
	if err != nil {
		return err
	}
	defer func() { _ = registryRoot.Close() }()
	var indexes, manifests []filecache.File
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "deps/") && !strings.HasPrefix(file.Path, "registry/") {
			continue
		}
		switch path.Dir(file.Path) {
		case "deps/vmconfig/manifests", "deps/components/manifests", "deps/templates/manifests", "deps/seeds/manifests":
			manifests = append(manifests, file)
			continue
		}
		if path.Base(file.Path) == "index.json" && (strings.HasPrefix(file.Path, "registry/") || strings.HasPrefix(file.Path, "deps/templates/charts/")) {
			indexes = append(indexes, file)
			continue
		}
		root, dest := importDestination(depsRoot, registryRoot, file.Path)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := file.Copy(src, root, dest); err != nil {
			return err
		}
	}
	for _, file := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		root, dest := importDestination(depsRoot, registryRoot, file.Path)
		if err := mergeIndex(src, root, file.Path, dest); err != nil {
			return err
		}
	}
	for _, file := range manifests {
		if err := ctx.Err(); err != nil {
			return err
		}
		root, dest := importDestination(depsRoot, registryRoot, file.Path)
		if err := file.Copy(src, root, dest); err != nil {
			return err
		}
	}
	_, err = m.VerifyOffline(kind, arch, providers, addons)
	return err
}

// importDestination maps a dependency or registry inventory path to its cache
// root and the path relative to that root.
func importDestination(deps, registry *os.Root, name string) (*os.Root, string) {
	if rest, ok := strings.CutPrefix(name, "registry/"); ok {
		return registry, rest
	}
	return deps, strings.TrimPrefix(name, "deps/")
}

// mergeIndex retains unrelated tags and platform manifests. A tag's
// locally synthesized multi-platform index is rebuilt, never blindly replaced.
func mergeIndex(src, dst *os.Root, source, dest string) error {
	var incoming, existing ociIndex
	raw, err := src.ReadFile(source)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return err
	}
	raw, err = dst.ReadFile(dest)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return err
		}
	} else {
		existing = ociIndex{SchemaVersion: 2, MediaType: incoming.MediaType}
	}
	for _, entry := range incoming.Manifests {
		tag := entry.Annotations["org.opencontainers.image.ref.name"]
		for _, old := range existing.Manifests {
			if tag == "" || old.Annotations["org.opencontainers.image.ref.name"] != tag || old.Digest == entry.Digest {
				continue
			}
			if strings.HasSuffix(old.MediaType, "index.v1+json") && strings.HasSuffix(entry.MediaType, "index.v1+json") {
				var a, b ociIndex
				oldBlob := path.Join(path.Dir(dest), "blobs/sha256", strings.TrimPrefix(old.Digest, "sha256:"))
				newBlob := path.Join(path.Dir(source), "blobs/sha256", strings.TrimPrefix(entry.Digest, "sha256:"))
				oldRaw, err := dst.ReadFile(oldBlob)
				if err != nil {
					return err
				}
				newRaw, err := src.ReadFile(newBlob)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(oldRaw, &a); err != nil {
					return err
				}
				if err := json.Unmarshal(newRaw, &b); err != nil {
					return err
				}
				for _, child := range b.Manifests {
					upsertManifestDescriptor(&a, child)
				}
				merged, err := json.Marshal(a)
				if err != nil {
					return err
				}
				entry.Digest, entry.Size = digestBytes(merged), int64(len(merged))
				name := path.Join(path.Dir(dest), "blobs/sha256", strings.TrimPrefix(entry.Digest, "sha256:"))
				if err := filecache.WriteFile(dst, name, 0644, func(w io.Writer) error { _, err := w.Write(merged); return err }); err != nil {
					return err
				}
			}
		}
		upsertIndexDescriptor(&existing, entry)
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	return filecache.WriteFile(dst, dest, 0644, func(w io.Writer) error { _, err := w.Write(merged); return err })
}
