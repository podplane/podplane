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

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/podplane/podplane/internal/filecache"
)

// Export copies selected dependencies to a separate cache without downloading.
// It preserves the cache layout, omits unused architectures and historical
// manifests, and pins the chart source to the cached release.
func (m *Manager) Export(dst *Manager, kind, arch string, providers, addons []string) error {
	manifest, _, err := m.ReadCachedManifest(kind, arch)
	if err != nil || manifest == nil {
		return fmt.Errorf("read bundle vmconfig manifest: %v", err)
	}
	for _, item := range manifest.DownloadItems(ItemFilter{Providers: providers}) {
		if err := filecache.Copy(m.VMConfigArtifactCachePath(item.Name, item.Dep), dst.VMConfigArtifactCachePath(item.Name, item.Dep)); err != nil {
			return err
		}
	}
	images := []ComponentImage{}
	for _, image := range manifest.VMConfig.Images {
		images = append(images, vmconfigComponentImage(image))
	}
	components, err := m.CachedComponentsManifest()
	if err != nil {
		return err
	}
	components.ResetCached()
	for _, i := range components.DownloadImageIndexes(ComponentImageFilter{Archs: []string{arch}, Providers: providers, Addons: addons}) {
		images = append(images, components.Components.Images[i])
		components.MarkCached(i)
	}
	if components.Components.Source != nil {
		src, err := m.ComponentsGitCachePath(components.Components.Source.URL)
		if err != nil {
			return err
		}
		dest, err := dst.ComponentsGitCachePath(components.Components.Source.URL)
		if err != nil {
			return err
		}
		if err := filecache.CopyTree(src, dest); err != nil {
			return err
		}
		// Freeze the chart source to the same release as the cached images and
		// seed, rather than letting Flux select a newer tag from the mirror.
		repo, err := git.PlainOpen(dest)
		if err != nil {
			return err
		}
		defer func() { _ = repo.Close() }()
		source := components.Components.Source
		ref := source.Ref.Commit
		switch {
		case components.Components.Version != "" && components.Components.Version != "dev":
			ref = "refs/tags/" + components.Components.Version
			if _, err := repo.ResolveRevision(plumbing.Revision(ref)); err != nil {
				ref = "refs/tags/v" + strings.TrimPrefix(components.Components.Version, "v")
			}
		case source.Ref.Tag != "":
			ref = "refs/tags/" + source.Ref.Tag
		case source.Ref.Branch != "":
			ref = "refs/heads/" + source.Ref.Branch
		}
		if ref == "" {
			return fmt.Errorf("cannot pin components source without a release version or exact ref")
		}
		commit, err := repo.ResolveRevision(plumbing.Revision(ref))
		if err != nil {
			return fmt.Errorf("pin components source %s: %w", ref, err)
		}
		source.Ref = ComponentsSourceRef{Commit: commit.String()}
	}
	templates, _, err := m.ReadCachedTemplatesManifest()
	if err != nil || templates == nil {
		return fmt.Errorf("read bundle templates manifest: %v", err)
	}
	for i := range templates.Templates.Images {
		templates.Templates.Images[i].Cached = false
	}
	for _, i := range templates.DownloadImageIndexes([]string{arch}) {
		images = append(images, templateComponentImage(templates.Templates.Images[i]))
		templates.MarkImageCached(i)
	}
	if len(templates.Templates.Charts) > 0 {
		if err := filecache.CopyTree(m.TemplatesChartsCacheDir(), dst.TemplatesChartsCacheDir()); err != nil {
			return err
		}
	}
	seeds, _, err := m.ReadCachedSeedsManifest()
	if err != nil || seeds == nil {
		return fmt.Errorf("read bundle seeds manifest: %v", err)
	}
	for name, snapshot := range seeds.Seeds.Snapshots {
		if err := filecache.Copy(m.seedSnapshotCachePath(seeds, name, snapshot), dst.seedSnapshotCachePath(seeds, name, snapshot)); err != nil {
			return err
		}
	}
	for _, image := range images {
		if err := copyCachedImage(m.RegistryCacheDir(), dst.RegistryCacheDir(), image); err != nil {
			return err
		}
	}
	for filename, value := range map[string]any{
		dst.VMConfigManifestCachePath(kind, arch): manifest,
		dst.ComponentsManifestCachePath():         components,
		dst.TemplatesManifestCachePath():          templates,
		dst.SeedsManifestCachePath():              seeds,
	} {
		if err := filecache.WriteJSON(filename, value); err != nil {
			return err
		}
	}
	return nil
}

// copyCachedImage copies an OCI image's blobs and updates the destination index,
// retaining any upstream index digest referenced by the selected image.
func copyCachedImage(source, dest string, image ComponentImage) error {
	repo := filepath.FromSlash(mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image))
	src, dst := filepath.Join(source, repo), filepath.Join(dest, repo)
	if err := ensureOCIRepo(dst); err != nil {
		return err
	}
	raw, err := os.ReadFile(blobPath(src, image.Digest))
	if err != nil {
		return err
	}
	var manifest struct {
		MediaType string          `json:"mediaType"`
		Config    ociDescriptor   `json:"config"`
		Layers    []ociDescriptor `json:"layers"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	for _, desc := range append(manifest.Layers, manifest.Config, ociDescriptor{Digest: image.Digest}) {
		if desc.Digest != "" {
			if err := filecache.Copy(blobPath(src, desc.Digest), blobPath(dst, desc.Digest)); err != nil {
				return err
			}
		}
	}
	desc := ociDescriptor{MediaType: manifest.MediaType, Digest: image.Digest, Size: int64(len(raw))}
	if parts := strings.Split(image.Platform, "/"); len(parts) == 2 {
		desc.Platform = &ociPlatform{OS: parts[0], Architecture: parts[1]}
	}
	_, tag, pinned := splitImageRef(image.Image)
	if pinned != "" && pinned != image.Digest {
		// Preserve the upstream index digest used by charts, even though the kit
		// intentionally contains only the selected platform's child manifest.
		if err := filecache.Copy(blobPath(src, pinned), blobPath(dst, pinned)); err != nil {
			return err
		}
		index, err := readRepoIndex(src)
		if err != nil {
			return err
		}
		for _, entry := range index.Manifests {
			if entry.Digest == pinned {
				if err := upsertRepoIndexDescriptor(dst, entry); err != nil {
					return err
				}
			}
		}
	}
	if tag != "" && (pinned == "" || pinned == image.Digest) {
		return upsertTaggedImageIndex(dst, tag, desc)
	}
	return upsertRepoIndexDescriptor(dst, desc)
}
