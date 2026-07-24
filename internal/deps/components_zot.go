// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const zotRootDirectory = "."
const defaultMirrorPrefix = "mirror"

var ociLayoutJSON = []byte(`{"imageLayoutVersion":"1.0.0"}`)

type ociDescriptor struct {
	MediaType   string            `json:"mediaType,omitempty"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Platform    *ociPlatform      `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ociPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType,omitempty"`
	Manifests     []ociDescriptor `json:"manifests,omitempty"`
}

type componentImageProgress struct {
	name    string
	current int64
	total   int64
	emit    func(DownloadEvent)
}

func (p *componentImageProgress) addCurrent(size int64) {
	if p == nil || size <= 0 {
		return
	}
	p.current += size
	p.report()
}

func (p *componentImageProgress) report() {
	if p == nil || p.emit == nil {
		return
	}
	p.emit(DownloadEvent{Type: DownloadEventProgress, Name: p.name, Current: p.current, Total: p.total})
}

type progressReadCloser struct {
	io.ReadCloser
	read func(int)
}

func (r progressReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.read != nil {
		r.read(n)
	}
	return n, err
}

// PopulateComponentsZotStore downloads component images and writes zot's
// repository-local OCI layout directly under destDir.
func PopulateComponentsZotStore(ctx context.Context, destDir string, manifest *ComponentsManifest, progress func(DownloadEvent)) error {
	if manifest == nil {
		return fmt.Errorf("components manifest is required")
	}
	if destDir == "" {
		return fmt.Errorf("components cache dir is required")
	}
	for _, image := range manifest.Components.Images {
		if image.Image == "" {
			return fmt.Errorf("component %s has empty image", image.componentLabel())
		}
		if image.Digest == "" {
			return fmt.Errorf("component %s image %s has empty digest", image.componentLabel(), image.Image)
		}
		if image.Size <= 0 {
			return fmt.Errorf("component %s image %s has missing size", image.componentLabel(), image.Image)
		}
		cached, err := componentImageCached(destDir, image)
		if err != nil {
			return err
		}
		if cached {
			if progress != nil {
				progress(DownloadEvent{Type: DownloadEventCached, Name: image.Image, Current: image.Size, Total: image.Size})
			}
			continue
		}
		if progress != nil {
			progress(DownloadEvent{Type: DownloadEventStarted, Name: image.Image, Message: "Downloading component image"})
		}
		imageProgress := &componentImageProgress{name: image.Image, total: image.Size, emit: progress}
		imageProgress.report()
		if err := writeComponentImage(ctx, destDir, image, imageProgress); err != nil {
			if progress != nil {
				progress(DownloadEvent{Type: DownloadEventFailed, Name: image.Image, Err: err})
			}
			return err
		}
		if progress != nil {
			progress(DownloadEvent{Type: DownloadEventDone, Name: image.Image, Current: imageProgress.current, Total: imageProgress.total})
		}
	}
	return nil
}

// writeComponentImage writes one component image into zot's repository-local OCI layout.
func writeComponentImage(ctx context.Context, destDir string, image ComponentImage, progress *componentImageProgress) error {
	resolvedImageRef := resolvedImage(image.Image, image.Digest)
	resolved, err := name.ParseReference(resolvedImageRef)
	if err != nil {
		return fmt.Errorf("parse resolved image %q: %w", resolvedImageRef, err)
	}
	chartRef, err := name.ParseReference(image.Image, name.WeakValidation)
	if err != nil {
		return fmt.Errorf("parse image %q: %w", image.Image, err)
	}
	_, chartTag, chartDigest := splitImageRef(image.Image)
	chartDescriptorRef := chartRef
	if chartDigest != "" {
		chartDescriptorRef, err = name.ParseReference(resolvedImage(image.Image, chartDigest))
		if err != nil {
			return fmt.Errorf("parse chart descriptor image %q: %w", image.Image, err)
		}
	}
	repo := mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)
	repoDir := filepath.Join(destDir, zotRootDirectory, filepath.FromSlash(repo))
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		return fmt.Errorf("create zot repo %s: %w", repo, err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "oci-layout"), ociLayoutJSON, 0o644); err != nil {
		return fmt.Errorf("write oci-layout for %s: %w", repo, err)
	}

	desc, err := remote.Get(resolved, remote.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("fetch descriptor for %s: %w", resolvedImageRef, err)
	}
	if chartDigest != "" && chartDigest != image.Digest {
		chartDesc, err := remote.Get(chartDescriptorRef, remote.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("fetch descriptor for %s: %w", image.Image, err)
		}
		if len(chartDesc.Manifest) > 0 {
			if err := writeDigestBlob(repoDir, chartDesc.Digest.String(), chartDesc.Manifest); err != nil {
				return err
			}
			progress.addCurrent(int64(len(chartDesc.Manifest)))
		}
		if err := writeDescriptorBlobs(ctx, repoDir, resolved.Context(), desc, progress); err != nil {
			return fmt.Errorf("write blobs for %s: %w", resolvedImageRef, err)
		}
		if chartTag != "" {
			if err := upsertRepoIndexDescriptor(repoDir, descriptorFromRemote(desc, image.Platform)); err != nil {
				return err
			}
			return upsertTaggedImageDescriptor(repoDir, chartTag, descriptorFromRemote(chartDesc, ""))
		}
		index, err := readRepoIndex(repoDir)
		if err != nil {
			return err
		}
		upsertIndexDescriptor(&index, descriptorFromRemote(chartDesc, ""))
		return writeRepoIndex(repoDir, index)
	}
	if err := writeDescriptorBlobs(ctx, repoDir, resolved.Context(), desc, progress); err != nil {
		return fmt.Errorf("write blobs for %s: %w", resolvedImageRef, err)
	}

	if chartTag == "" {
		index, err := readRepoIndex(repoDir)
		if err != nil {
			return err
		}
		upsertIndexDescriptor(&index, descriptorFromRemote(desc, image.Platform))
		return writeRepoIndex(repoDir, index)
	}
	return upsertTaggedImageIndex(repoDir, chartTag, descriptorFromRemote(desc, image.Platform))
}

func componentImageCached(destDir string, image ComponentImage) (bool, error) {
	_, err := name.ParseReference(image.Image, name.WeakValidation)
	if err != nil {
		return false, fmt.Errorf("parse image %q: %w", image.Image, err)
	}
	repo := mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)
	repoDir := filepath.Join(destDir, zotRootDirectory, filepath.FromSlash(repo))
	_, wantTag, chartDigest := splitImageRef(image.Image)
	if wantTag != "" {
		repoIndex, err := readRepoIndex(repoDir)
		if err != nil {
			return false, err
		}
		index, err := readTaggedImageIndex(repoDir, wantTag)
		if err != nil {
			return false, err
		}
		for _, entry := range index.Manifests {
			if entry.Digest != image.Digest && imageMatchesDescriptorPlatform(image, entry) {
				return false, nil
			}
			if entry.Digest != image.Digest {
				continue
			}
			if !indexHasDigest(repoIndex, entry.Digest) {
				return false, nil
			}
			complete, err := descriptorBlobsComplete(repoDir, entry.Digest)
			if err != nil || !complete {
				return complete, err
			}
			if chartDigest != "" && chartDigest != image.Digest {
				return descriptorBlobsComplete(repoDir, chartDigest)
			}
			return true, nil
		}
		return false, nil
	}
	index, err := readRepoIndex(repoDir)
	if err != nil {
		return false, err
	}
	for _, entry := range index.Manifests {
		if entry.Digest != image.Digest {
			continue
		}
		return descriptorBlobsComplete(repoDir, entry.Digest)
	}
	return false, nil
}

// imageMatchesDescriptorPlatform reports whether a cached descriptor targets
// the same OS and architecture as a component image.
func imageMatchesDescriptorPlatform(image ComponentImage, desc ociDescriptor) bool {
	if image.Platform == "" || desc.Platform == nil {
		return false
	}
	parts := strings.Split(image.Platform, "/")
	if len(parts) < 2 {
		return false
	}
	return desc.Platform.OS == parts[0] && desc.Platform.Architecture == parts[1]
}

// indexHasDigest reports whether an OCI index contains a descriptor digest.
func indexHasDigest(index ociIndex, digest string) bool {
	for _, entry := range index.Manifests {
		if entry.Digest == digest {
			return true
		}
	}
	return false
}

// resolvedImage returns the digest-pinned source image reference for an image entry.
func resolvedImage(image, digest string) string {
	repo, _, _ := splitImageRef(image)
	return repo + "@" + digest
}

// mirrorRepoFromChartImage returns the zot repository path for a rendered source image under prefix.
func mirrorRepoFromChartImage(prefix, chartImage string) string {
	image, _, _ := splitImageRef(chartImage)
	first := image
	if slash := strings.Index(image, "/"); slash >= 0 {
		first = image[:slash]
	}
	var repo string
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		repo = image
	} else if strings.Contains(image, "/") {
		repo = "docker.io/" + image
	} else {
		repo = "docker.io/library/" + image
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return repo
	}
	return prefix + "/" + repo
}

// MirroredImageRef returns the explicit image reference served by mirrorHost
// for a source image cached with Podplane's zot repository layout.
func MirroredImageRef(mirrorHost, mirrorPrefix, image string) string {
	repo, tag, digest := splitImageRef(image)
	repo = mirrorRepoFromChartImage(mirrorPrefix, repo)
	ref := mirrorHost + "/" + repo
	if tag != "" {
		ref += ":" + tag
	}
	if digest != "" {
		ref += "@" + digest
	}
	return ref
}

// splitImageRef splits an image reference into repository, tag, and digest parts.
func splitImageRef(image string) (repo string, tag string, digest string) {
	if before, after, ok := strings.Cut(image, "@"); ok {
		image = before
		digest = after
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		tag = image[lastColon+1:]
		image = image[:lastColon]
	}
	return image, tag, digest
}

// writeDescriptorBlobs recursively writes an image descriptor and referenced blobs.
func writeDescriptorBlobs(ctx context.Context, repoDir string, repo name.Repository, desc *remote.Descriptor, progress *componentImageProgress) error {
	if len(desc.Manifest) > 0 {
		if err := writeDigestBlob(repoDir, desc.Digest.String(), desc.Manifest); err != nil {
			return err
		}
		progress.addCurrent(int64(len(desc.Manifest)))
	}
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return err
		}
		manifest, err := idx.IndexManifest()
		if err != nil {
			return err
		}
		for _, child := range manifest.Manifests {
			childDesc, err := remote.Get(childDigestReference(repo, child.Digest), remote.WithContext(ctx))
			if err != nil {
				return err
			}
			if err := writeDescriptorBlobs(ctx, repoDir, repo, childDesc, progress); err != nil {
				return err
			}
		}
		return nil
	}
	img, err := desc.Image()
	if err != nil {
		return err
	}
	config, err := img.RawConfigFile()
	if err != nil {
		return err
	}
	configName, err := img.ConfigName()
	if err != nil {
		return err
	}
	if err := writeDigestBlob(repoDir, configName.String(), config); err != nil {
		return err
	}
	progress.addCurrent(int64(len(config)))
	layers, err := img.Layers()
	if err != nil {
		return err
	}
	for _, layer := range layers {
		digest, err := layer.Digest()
		if err != nil {
			return err
		}
		size, err := layer.Size()
		if err != nil {
			return err
		}
		path := blobPath(repoDir, digest.String())
		complete, err := existingBlobComplete(path, digest.String(), size)
		if err != nil {
			return err
		}
		if complete {
			progress.addCurrent(size)
			continue
		}
		r, err := layer.Compressed()
		if err != nil {
			return err
		}
		r = progressReadCloser{ReadCloser: r, read: func(n int) { progress.addCurrent(int64(n)) }}
		if err := writeReader(path, r); err != nil {
			return err
		}
	}
	return nil
}

func existingBlobComplete(path, digest string, size int64) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Size() != size {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	return got == digest, nil
}

// descriptorFromRemote converts a remote descriptor to the local OCI descriptor shape.
func descriptorFromRemote(desc *remote.Descriptor, platform string) ociDescriptor {
	return ociDescriptor{
		MediaType: string(desc.MediaType),
		Digest:    desc.Digest.String(),
		Size:      desc.Size,
		Platform:  ociPlatformFromString(platform),
	}
}

// ociPlatformFromString parses an OCI platform string into a descriptor platform.
func ociPlatformFromString(platform string) *ociPlatform {
	parts := strings.Split(platform, "/")
	if len(parts) < 2 {
		return nil
	}
	value := &ociPlatform{OS: parts[0], Architecture: parts[1]}
	if len(parts) > 2 {
		value.Variant = parts[2]
	}
	return value
}

// descriptorBlobsComplete verifies a manifest descriptor and every referenced blob.
func descriptorBlobsComplete(repoDir, digest string) (bool, error) {
	body, err := os.ReadFile(blobPath(repoDir, digest))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	got := sha256.Sum256(body)
	if digest != "sha256:"+hex.EncodeToString(got[:]) {
		return false, nil
	}
	var manifest struct {
		Config *struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return false, err
	}
	if manifest.Config != nil {
		complete, err := existingBlobComplete(blobPath(repoDir, manifest.Config.Digest), manifest.Config.Digest, manifest.Config.Size)
		if err != nil || !complete {
			return complete, err
		}
	}
	for _, layer := range manifest.Layers {
		complete, err := existingBlobComplete(blobPath(repoDir, layer.Digest), layer.Digest, layer.Size)
		if err != nil || !complete {
			return complete, err
		}
	}
	return true, nil
}

// childDigestReference returns a digest reference in the same repository as the parent index.
func childDigestReference(repo name.Repository, digest v1.Hash) name.Digest {
	return repo.Digest(digest.String())
}

// writeDigestBlob writes a small blob after verifying its sha256 digest.
func writeDigestBlob(repoDir, digest string, body []byte) error {
	got := sha256.Sum256(body)
	if digest != "sha256:"+hex.EncodeToString(got[:]) {
		return fmt.Errorf("blob digest mismatch: got sha256:%s want %s", hex.EncodeToString(got[:]), digest)
	}
	return os.WriteFile(blobPath(repoDir, digest), body, 0o644)
}

// writeReader atomically writes a streamed blob to disk.
func writeReader(path string, r io.ReadCloser) error {
	defer func() { _ = r.Close() }()
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, path)
}

// blobPath returns zot's blob path for a digest inside a repository directory.
func blobPath(repoDir, digest string) string {
	return filepath.Join(repoDir, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
}

// readRepoIndex reads a repository index.json or returns an empty OCI index.
func readRepoIndex(repoDir string) (ociIndex, error) {
	path := filepath.Join(repoDir, "index.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ociIndex{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json"}, nil
	}
	if err != nil {
		return ociIndex{}, err
	}
	var index ociIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		return ociIndex{}, err
	}
	if index.SchemaVersion == 0 {
		index.SchemaVersion = 2
	}
	return index, nil
}

// readTaggedImageIndex reads the local image index currently associated with a tag.
func readTaggedImageIndex(repoDir, tag string) (ociIndex, error) {
	repoIndex, err := readRepoIndex(repoDir)
	if err != nil {
		return ociIndex{}, err
	}
	for _, entry := range repoIndex.Manifests {
		if entry.Annotations["org.opencontainers.image.ref.name"] != tag {
			continue
		}
		raw, err := os.ReadFile(blobPath(repoDir, entry.Digest))
		if os.IsNotExist(err) {
			return ociIndex{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json"}, nil
		}
		if err != nil {
			return ociIndex{}, err
		}
		var index ociIndex
		if err := json.Unmarshal(raw, &index); err != nil {
			return ociIndex{}, err
		}
		if index.SchemaVersion == 0 {
			index.SchemaVersion = 2
		}
		if index.MediaType == "" {
			index.MediaType = "application/vnd.oci.image.index.v1+json"
		}
		if len(index.Manifests) == 0 && entry.MediaType != "application/vnd.oci.image.index.v1+json" {
			return ociIndex{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json", Manifests: []ociDescriptor{entry}}, nil
		}
		return index, nil
	}
	return ociIndex{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json"}, nil
}

// upsertTaggedImageIndex adds or replaces a platform manifest in a tag's local index.
func upsertTaggedImageIndex(repoDir, tag string, manifest ociDescriptor) error {
	imageIndex, err := readTaggedImageIndex(repoDir, tag)
	if err != nil {
		return err
	}
	upsertManifestDescriptor(&imageIndex, manifest)
	raw, err := json.Marshal(imageIndex)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	indexDigest := "sha256:" + hex.EncodeToString(digest[:])
	if err := os.WriteFile(blobPath(repoDir, indexDigest), raw, 0o644); err != nil {
		return err
	}
	repoIndex, err := readRepoIndex(repoDir)
	if err != nil {
		return err
	}
	upsertIndexDescriptor(&repoIndex, manifest)
	upsertIndexDescriptor(&repoIndex, ociDescriptor{
		MediaType:   "application/vnd.oci.image.index.v1+json",
		Digest:      indexDigest,
		Size:        int64(len(raw)),
		Annotations: map[string]string{"org.opencontainers.image.ref.name": tag},
	})
	return writeRepoIndex(repoDir, repoIndex)
}

// upsertTaggedImageDescriptor points a tag at an existing image descriptor.
func upsertTaggedImageDescriptor(repoDir, tag string, desc ociDescriptor) error {
	repoIndex, err := readRepoIndex(repoDir)
	if err != nil {
		return err
	}
	if desc.Annotations == nil {
		desc.Annotations = map[string]string{}
	}
	desc.Annotations["org.opencontainers.image.ref.name"] = tag
	upsertIndexDescriptor(&repoIndex, desc)
	return writeRepoIndex(repoDir, repoIndex)
}

// upsertRepoIndexDescriptor adds or replaces a descriptor in the repo index.
func upsertRepoIndexDescriptor(repoDir string, desc ociDescriptor) error {
	repoIndex, err := readRepoIndex(repoDir)
	if err != nil {
		return err
	}
	upsertIndexDescriptor(&repoIndex, desc)
	return writeRepoIndex(repoDir, repoIndex)
}

// writeRepoIndex writes a repository index.json file.
func writeRepoIndex(repoDir string, index ociIndex) error {
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoDir, "index.json"), append(raw, '\n'), 0o644)
}

// upsertIndexDescriptor adds or replaces an image descriptor in an OCI index.
func upsertIndexDescriptor(index *ociIndex, entry ociDescriptor) {
	refName := ""
	if entry.Annotations != nil {
		refName = entry.Annotations["org.opencontainers.image.ref.name"]
	}
	for i, existing := range index.Manifests {
		existingRef := ""
		if existing.Annotations != nil {
			existingRef = existing.Annotations["org.opencontainers.image.ref.name"]
		}
		if existing.Digest == entry.Digest || (refName != "" && existingRef == refName) {
			index.Manifests[i] = entry
			return
		}
	}
	index.Manifests = append(index.Manifests, entry)
}

// upsertManifestDescriptor adds or replaces a platform manifest descriptor in an image index.
func upsertManifestDescriptor(index *ociIndex, entry ociDescriptor) {
	for i, existing := range index.Manifests {
		if existing.Digest == entry.Digest || samePlatform(existing.Platform, entry.Platform) {
			index.Manifests[i] = entry
			return
		}
	}
	index.Manifests = append(index.Manifests, entry)
}

// samePlatform reports whether two OCI platform descriptors identify the same platform.
func samePlatform(a, b *ociPlatform) bool {
	if a == nil || b == nil {
		return false
	}
	return a.OS == b.OS && a.Architecture == b.Architecture && (a.Variant == b.Variant || a.Variant == "" || b.Variant == "")
}
