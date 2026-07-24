// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMirrorRepoFromChartImagePreservesRenderedRepoPath(t *testing.T) {
	tests := map[string]string{
		"quay.io/cilium/cilium:v1.16.3@sha256:abc":                   "mirror/quay.io/cilium/cilium",
		"ghcr.io/fluxcd/source-controller:v1.8.2":                    "mirror/ghcr.io/fluxcd/source-controller",
		"registry.k8s.io/sig-storage/snapshot-controller@sha256:abc": "mirror/registry.k8s.io/sig-storage/snapshot-controller",
		"docker.io/traefik:v3.4.3":                                   "mirror/docker.io/traefik",
		"coredns/coredns:1.11.3":                                     "mirror/docker.io/coredns/coredns",
		"caddy:latest":                                               "mirror/docker.io/library/caddy",
		"localhost:5000/example/app:tag":                             "mirror/localhost:5000/example/app",
	}
	for input, want := range tests {
		if got := mirrorRepoFromChartImage(defaultMirrorPrefix, input); got != want {
			t.Fatalf("mirrorRepoFromChartImage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMirroredImageRef(t *testing.T) {
	tests := map[string]string{
		"docker.io/library/caddy:2":           "zot.local/mirror/docker.io/library/caddy:2",
		"caddy:latest":                        "zot.local/mirror/docker.io/library/caddy:latest",
		"coredns/coredns:1.11.3":              "zot.local/mirror/docker.io/coredns/coredns:1.11.3",
		"ghcr.io/podplane/hello@sha256:abc":   "zot.local/mirror/ghcr.io/podplane/hello@sha256:abc",
		"quay.io/cilium/cilium:v1@sha256:def": "zot.local/mirror/quay.io/cilium/cilium:v1@sha256:def",
		"localhost:5000/example/app:tag":      "zot.local/mirror/localhost:5000/example/app:tag",
	}
	for input, want := range tests {
		if got := MirroredImageRef("zot.local", "mirror", input); got != want {
			t.Fatalf("MirroredImageRef(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRegistryHostFromImage(t *testing.T) {
	tests := map[string]string{
		"public.ecr.aws/csi-components/csi-attacher:v4.9.0-eksbuild.3": "public.ecr.aws",
		"quay.io/cilium/cilium:v1.16.3@sha256:abc":                     "quay.io",
		"docker.io/traefik:v3.4.3":                                     "docker.io",
		"coredns/coredns:1.11.3":                                       "docker.io",
		"caddy:latest":                                                 "docker.io",
		"localhost:5000/example/app:tag":                               "localhost:5000",
	}
	for input, want := range tests {
		if got := registryHostFromImage(input); got != want {
			t.Fatalf("registryHostFromImage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolvedImageDropsTagAndAppendsDigest(t *testing.T) {
	got := resolvedImage("quay.io/cilium/cilium:v1.16.3", "sha256:abc")
	want := "quay.io/cilium/cilium@sha256:abc"
	if got != want {
		t.Fatalf("resolvedImage = %q, want %q", got, want)
	}
}

func TestPopulateComponentsZotStoreRequiresImageSize(t *testing.T) {
	manifest := &ComponentsManifest{Components: Components{Images: []ComponentImage{{
		Components: []string{"test"},
		Image:      "example.com/test/app:v1",
		Digest:     "sha256:abc",
	}}}}
	err := PopulateComponentsZotStore(context.Background(), t.TempDir(), manifest, nil)
	if err == nil {
		t.Fatal("PopulateComponentsZotStore returned nil error, want missing size error")
	}
	if !strings.Contains(err.Error(), "has missing size") {
		t.Fatalf("PopulateComponentsZotStore error = %q, want missing size", err.Error())
	}
}

func TestExistingBlobCompleteChecksSizeAndDigest(t *testing.T) {
	dir := t.TempDir()
	body := []byte("blob contents")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	path := filepath.Join(dir, "blob")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}

	complete, err := existingBlobComplete(path, digest, int64(len(body)))
	if err != nil {
		t.Fatalf("existingBlobComplete returned error: %v", err)
	}
	if !complete {
		t.Fatal("existingBlobComplete returned false for complete blob")
	}

	complete, err = existingBlobComplete(path, digest, int64(len(body)+1))
	if err != nil {
		t.Fatalf("existingBlobComplete returned error for wrong size: %v", err)
	}
	if complete {
		t.Fatal("existingBlobComplete returned true for wrong size")
	}

	complete, err = existingBlobComplete(path, "sha256:"+strings.Repeat("0", 64), int64(len(body)))
	if err != nil {
		t.Fatalf("existingBlobComplete returned error for wrong digest: %v", err)
	}
	if complete {
		t.Fatal("existingBlobComplete returned true for wrong digest")
	}
}

func TestComponentImageCachedUsesLocalZotIndex(t *testing.T) {
	destDir := t.TempDir()
	body := []byte(`{"schemaVersion":2}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	image := ComponentImage{
		Components: []string{"test"},
		Image:      "example.com/test/app:v1",
		Digest:     digest,
		Size:       int64(len(body)),
	}
	repoDir := filepath.Join(destDir, zotRootDirectory, filepath.FromSlash(mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)))
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("create blobs dir: %v", err)
	}
	if err := writeDigestBlob(repoDir, digest, body); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	if err := writeRepoIndex(repoDir, ociIndex{SchemaVersion: 2, Manifests: []ociDescriptor{{
		Digest:      digest,
		Size:        int64(len(body)),
		Annotations: map[string]string{"org.opencontainers.image.ref.name": "v1"},
	}}}); err != nil {
		t.Fatalf("write index: %v", err)
	}

	cached, err := componentImageCached(destDir, image)
	if err != nil {
		t.Fatalf("componentImageCached returned error: %v", err)
	}
	if !cached {
		t.Fatal("componentImageCached returned false for indexed image")
	}

	wrongTag := image
	wrongTag.Image = "example.com/test/app:v2"
	cached, err = componentImageCached(destDir, wrongTag)
	if err != nil {
		t.Fatalf("componentImageCached returned error for wrong tag: %v", err)
	}
	if cached {
		t.Fatal("componentImageCached returned true for wrong tag")
	}
}

func TestComponentImageCachedRequiresPinnedChartDigest(t *testing.T) {
	destDir := t.TempDir()
	childBody := []byte(`{"schemaVersion":2}`)
	childSum := sha256.Sum256(childBody)
	childDigest := "sha256:" + hex.EncodeToString(childSum[:])
	indexBody := []byte(`{"schemaVersion":2,"manifests":[{"digest":"` + childDigest + `"}]}`)
	indexSum := sha256.Sum256(indexBody)
	indexDigest := "sha256:" + hex.EncodeToString(indexSum[:])
	image := ComponentImage{
		Components: []string{"test"},
		Image:      "example.com/test/app:v1@" + indexDigest,
		Digest:     childDigest,
		Size:       int64(len(childBody)),
	}
	repoDir := filepath.Join(destDir, zotRootDirectory, filepath.FromSlash(mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)))
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("create blobs dir: %v", err)
	}
	if err := writeDigestBlob(repoDir, childDigest, childBody); err != nil {
		t.Fatalf("write child blob: %v", err)
	}
	if err := upsertTaggedImageIndex(repoDir, "v1", ociDescriptor{Digest: childDigest, Size: int64(len(childBody))}); err != nil {
		t.Fatalf("upsert tagged index: %v", err)
	}

	cached, err := componentImageCached(destDir, image)
	if err != nil {
		t.Fatalf("componentImageCached returned error: %v", err)
	}
	if cached {
		t.Fatal("componentImageCached returned true without pinned chart digest blob")
	}

	if err := writeDigestBlob(repoDir, indexDigest, indexBody); err != nil {
		t.Fatalf("write index blob: %v", err)
	}
	cached, err = componentImageCached(destDir, image)
	if err != nil {
		t.Fatalf("componentImageCached returned error after index blob: %v", err)
	}
	if !cached {
		t.Fatal("componentImageCached returned false with child and pinned chart digest blobs")
	}
}

// TestComponentImageCachedRequiresTopLevelChildManifest verifies old tag-only
// repo indexes are repaired before zot serves them.
func TestComponentImageCachedRequiresTopLevelChildManifest(t *testing.T) {
	destDir := t.TempDir()
	childBody := []byte(`{"schemaVersion":2}`)
	childSum := sha256.Sum256(childBody)
	childDigest := "sha256:" + hex.EncodeToString(childSum[:])
	tagIndexBody := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"` + childDigest + `"}]}`)
	tagIndexSum := sha256.Sum256(tagIndexBody)
	tagIndexDigest := "sha256:" + hex.EncodeToString(tagIndexSum[:])
	image := ComponentImage{
		Components: []string{"test"},
		Image:      "example.com/test/app:v1",
		Digest:     childDigest,
		Size:       int64(len(childBody)),
	}
	repoDir := filepath.Join(destDir, zotRootDirectory, filepath.FromSlash(mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)))
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("create blobs dir: %v", err)
	}
	if err := writeDigestBlob(repoDir, childDigest, childBody); err != nil {
		t.Fatalf("write child blob: %v", err)
	}
	if err := writeDigestBlob(repoDir, tagIndexDigest, tagIndexBody); err != nil {
		t.Fatalf("write tag index blob: %v", err)
	}
	if err := writeRepoIndex(repoDir, ociIndex{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json", Manifests: []ociDescriptor{{
		MediaType:   "application/vnd.oci.image.index.v1+json",
		Digest:      tagIndexDigest,
		Size:        int64(len(tagIndexBody)),
		Annotations: map[string]string{"org.opencontainers.image.ref.name": "v1"},
	}}}); err != nil {
		t.Fatalf("write repo index: %v", err)
	}

	cached, err := componentImageCached(destDir, image)
	if err != nil {
		t.Fatalf("componentImageCached returned error: %v", err)
	}
	if cached {
		t.Fatal("componentImageCached returned true without top-level child manifest")
	}

	if err := upsertRepoIndexDescriptor(repoDir, ociDescriptor{Digest: childDigest, Size: int64(len(childBody))}); err != nil {
		t.Fatalf("upsert child manifest: %v", err)
	}
	cached, err = componentImageCached(destDir, image)
	if err != nil {
		t.Fatalf("componentImageCached returned error after child descriptor: %v", err)
	}
	if !cached {
		t.Fatal("componentImageCached returned false with top-level child manifest")
	}
}

// TestComponentImageCachedRejectsDuplicateSameArchManifest verifies stale tag
// indexes with duplicate architecture entries are repaired.
func TestComponentImageCachedRejectsDuplicateSameArchManifest(t *testing.T) {
	destDir := t.TempDir()
	oldBody := []byte(`{"schemaVersion":2,"old":true}`)
	oldSum := sha256.Sum256(oldBody)
	oldDigest := "sha256:" + hex.EncodeToString(oldSum[:])
	newBody := []byte(`{"schemaVersion":2,"old":false}`)
	newSum := sha256.Sum256(newBody)
	newDigest := "sha256:" + hex.EncodeToString(newSum[:])
	image := ComponentImage{
		Components: []string{"test"},
		Image:      "example.com/test/app:v1",
		Digest:     newDigest,
		Size:       int64(len(newBody)),
		Platform:   "linux/arm64/v8",
	}
	repoDir := filepath.Join(destDir, zotRootDirectory, filepath.FromSlash(mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)))
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("create blobs dir: %v", err)
	}
	if err := writeDigestBlob(repoDir, oldDigest, oldBody); err != nil {
		t.Fatalf("write old blob: %v", err)
	}
	if err := writeDigestBlob(repoDir, newDigest, newBody); err != nil {
		t.Fatalf("write new blob: %v", err)
	}
	if err := upsertTaggedImageIndex(repoDir, "v1", ociDescriptor{Digest: oldDigest, Size: int64(len(oldBody)), Platform: &ociPlatform{OS: "linux", Architecture: "arm64"}}); err != nil {
		t.Fatalf("upsert old manifest: %v", err)
	}
	if err := upsertTaggedImageIndex(repoDir, "v1", ociDescriptor{Digest: newDigest, Size: int64(len(newBody)), Platform: &ociPlatform{OS: "linux", Architecture: "arm64", Variant: "v8"}}); err != nil {
		t.Fatalf("upsert new manifest: %v", err)
	}

	index, err := readTaggedImageIndex(repoDir, "v1")
	if err != nil {
		t.Fatalf("read tagged index: %v", err)
	}
	if len(index.Manifests) != 1 || index.Manifests[0].Digest != newDigest {
		t.Fatalf("tagged index manifests = %#v, want only %s", index.Manifests, newDigest)
	}
	cached, err := componentImageCached(destDir, image)
	if err != nil {
		t.Fatalf("componentImageCached returned error: %v", err)
	}
	if !cached {
		t.Fatal("componentImageCached returned false after duplicate repair")
	}
}

func TestTaggedImageIndexPreservesMultiplePlatforms(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("create blobs dir: %v", err)
	}
	amd64 := ociDescriptor{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    "sha256:" + strings.Repeat("a", 64),
		Size:      1,
		Platform:  &ociPlatform{OS: "linux", Architecture: "amd64"},
	}
	arm64 := ociDescriptor{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    "sha256:" + strings.Repeat("b", 64),
		Size:      1,
		Platform:  &ociPlatform{OS: "linux", Architecture: "arm64", Variant: "v8"},
	}

	if err := upsertTaggedImageIndex(repoDir, "v1", amd64); err != nil {
		t.Fatalf("upsert amd64: %v", err)
	}
	if err := upsertTaggedImageIndex(repoDir, "v1", arm64); err != nil {
		t.Fatalf("upsert arm64: %v", err)
	}

	index, err := readTaggedImageIndex(repoDir, "v1")
	if err != nil {
		t.Fatalf("read tagged index: %v", err)
	}
	if len(index.Manifests) != 2 {
		t.Fatalf("tagged index manifests = %d, want 2", len(index.Manifests))
	}
	repoIndex, err := readRepoIndex(repoDir)
	if err != nil {
		t.Fatalf("read repo index: %v", err)
	}
	var taggedIndexCount, childManifestCount int
	for _, entry := range repoIndex.Manifests {
		if entry.Annotations["org.opencontainers.image.ref.name"] == "v1" {
			taggedIndexCount++
			continue
		}
		if entry.Digest == amd64.Digest || entry.Digest == arm64.Digest {
			childManifestCount++
		}
	}
	if taggedIndexCount != 1 {
		t.Fatalf("repo index tagged descriptors = %d, want 1", taggedIndexCount)
	}
	if childManifestCount != 2 {
		t.Fatalf("repo index child manifest descriptors = %d, want 2", childManifestCount)
	}
}

func TestTaggedImageDescriptorPreservesOriginalDigest(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("create blobs dir: %v", err)
	}
	childDigest := "sha256:" + strings.Repeat("b", 64)
	imageIndex := ociIndex{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests: []ociDescriptor{{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    childDigest,
			Size:      1,
			Platform:  &ociPlatform{OS: "linux", Architecture: "arm64"},
		}},
	}
	raw, err := json.Marshal(imageIndex)
	if err != nil {
		t.Fatalf("marshal image index: %v", err)
	}
	sum := sha256.Sum256(raw)
	indexDigest := "sha256:" + hex.EncodeToString(sum[:])
	if err := writeDigestBlob(repoDir, indexDigest, raw); err != nil {
		t.Fatalf("write index blob: %v", err)
	}

	if err := upsertTaggedImageDescriptor(repoDir, "v1", ociDescriptor{
		MediaType: "application/vnd.oci.image.index.v1+json",
		Digest:    indexDigest,
		Size:      int64(len(raw)),
	}); err != nil {
		t.Fatalf("upsert tagged descriptor: %v", err)
	}

	repoIndex, err := readRepoIndex(repoDir)
	if err != nil {
		t.Fatalf("read repo index: %v", err)
	}
	if len(repoIndex.Manifests) != 1 {
		t.Fatalf("repo index manifests = %d, want 1", len(repoIndex.Manifests))
	}
	if got := repoIndex.Manifests[0].Digest; got != indexDigest {
		t.Fatalf("repo index digest = %q, want %q", got, indexDigest)
	}
	if got := repoIndex.Manifests[0].Annotations["org.opencontainers.image.ref.name"]; got != "v1" {
		t.Fatalf("repo index ref name = %q, want v1", got)
	}
	tagged, err := readTaggedImageIndex(repoDir, "v1")
	if err != nil {
		t.Fatalf("read tagged index: %v", err)
	}
	if len(tagged.Manifests) != 1 || tagged.Manifests[0].Digest != childDigest {
		t.Fatalf("tagged index manifests = %#v, want child digest %s", tagged.Manifests, childDigest)
	}
}
