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
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/podplane/podplane/internal/execwrap"
)

const (
	helmChartLayerMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	helmConfigMediaType     = "application/vnd.cncf.helm.config.v1+json"
	ociManifestMediaType    = "application/vnd.oci.image.manifest.v1+json"
)

// populateTemplatesCache caches every chart referenced by a templates manifest.
func (m *Manager) populateTemplatesCache(ctx context.Context, manifest *TemplatesManifest, manifestPath string, progress func(DownloadEvent)) error {
	if manifest == nil {
		return fmt.Errorf("templates manifest is required")
	}
	for i, chart := range manifest.Templates.Charts {
		switch chart.Type {
		case TemplateChartTypeOCI:
			layerDigest, cached, err := m.cacheOCITemplateChart(ctx, chart, progress)
			if err != nil {
				return err
			}
			if cached && progress != nil {
				progress(DownloadEvent{Type: DownloadEventCached, Name: chart.Name})
			}
			manifest.MarkCached(i, layerDigest)
		case TemplateChartTypeChart:
			layerDigest, err := m.cacheLocalTemplateChart(chart, manifestPath, progress)
			if err != nil {
				return err
			}
			manifest.MarkCached(i, layerDigest)
		default:
			return fmt.Errorf("template %s has unsupported type %q", chart.Name, chart.Type)
		}
	}
	return nil
}

// cacheOCITemplateChart downloads and caches one OCI-published template chart.
func (m *Manager) cacheOCITemplateChart(ctx context.Context, chart TemplateChart, progress func(DownloadEvent)) (string, bool, error) {
	if chart.Name == "" {
		return "", false, fmt.Errorf("template chart name is required")
	}
	if chart.URL == "" {
		return "", false, fmt.Errorf("template %s has empty OCI URL", chart.Name)
	}
	ref, err := templateOCIReference(chart)
	if err != nil {
		return "", false, err
	}
	repoDir := filepath.Join(m.TemplatesChartsCacheDir(), zotRootDirectory, filepath.FromSlash(templateChartRepo(chart)))
	if err := ensureOCIRepo(repoDir); err != nil {
		return "", false, err
	}
	desc, err := remote.Get(ref, remote.WithContext(ctx))
	if err != nil {
		return "", false, fmt.Errorf("fetch template chart %s: %w", chart.Name, err)
	}
	if chart.Digest != "" && desc.Digest.String() != chart.Digest {
		return "", false, fmt.Errorf("template %s digest mismatch: got %s want %s", chart.Name, desc.Digest.String(), chart.Digest)
	}
	layerDigest, layerSize, err := chartLayerFromDescriptor(desc)
	if err != nil {
		return "", false, fmt.Errorf("template %s: %w", chart.Name, err)
	}
	cached, err := descriptorBlobsComplete(repoDir, desc.Digest.String())
	if err != nil {
		return "", false, err
	}
	if cached {
		layerCached, err := existingBlobComplete(blobPath(repoDir, layerDigest), layerDigest, layerSize)
		if err != nil {
			return "", false, err
		}
		if layerCached {
			return layerDigest, true, nil
		}
	}
	if progress != nil {
		progress(DownloadEvent{Type: DownloadEventStarted, Name: chart.Name})
	}
	chartProgress := &componentImageProgress{name: chart.Name, total: desc.Size, emit: progress}
	if err := writeDescriptorBlobs(ctx, repoDir, ref.Context(), desc, chartProgress); err != nil {
		return "", false, err
	}
	if err := upsertTaggedImageDescriptor(repoDir, helmChartTag(chart.Version), descriptorFromRemote(desc, "")); err != nil {
		return "", false, err
	}
	if progress != nil {
		progress(DownloadEvent{Type: DownloadEventDone, Name: chart.Name, Current: chartProgress.current, Total: chartProgress.total})
	}
	return layerDigest, false, nil
}

// cacheLocalTemplateChart packages and caches one local unpacked chart.
func (m *Manager) cacheLocalTemplateChart(chart TemplateChart, manifestPath string, progress func(DownloadEvent)) (string, error) {
	if chart.Name == "" {
		return "", fmt.Errorf("template chart name is required")
	}
	if chart.Path == "" {
		return "", fmt.Errorf("template %s has empty chart path", chart.Name)
	}
	chartPath := chart.Path
	if !filepath.IsAbs(chartPath) {
		base := "."
		if manifestPath != "" {
			base = filepath.Dir(manifestPath)
		}
		chartPath = filepath.Clean(filepath.Join(base, chartPath))
	}
	tmpDir, err := os.MkdirTemp("", "podplane-template-chart-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if progress != nil {
		progress(DownloadEvent{Type: DownloadEventStarted, Name: chart.Name})
	}
	cmd := execwrap.Command("helm", "package", chartPath, "--destination", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("helm package %s: %w\n%s", chartPath, err, string(out))
	}
	archives, err := filepath.Glob(filepath.Join(tmpDir, "*.tgz"))
	if err != nil {
		return "", err
	}
	if len(archives) != 1 {
		return "", fmt.Errorf("helm package %s produced %d chart archives", chartPath, len(archives))
	}
	layer, err := os.ReadFile(archives[0])
	if err != nil {
		return "", err
	}
	repoDir := filepath.Join(m.TemplatesChartsCacheDir(), zotRootDirectory, filepath.FromSlash(templateChartRepo(chart)))
	if err := ensureOCIRepo(repoDir); err != nil {
		return "", err
	}
	layerDigest := digestBytes(layer)
	if err := writeDigestBlob(repoDir, layerDigest, layer); err != nil {
		return "", err
	}
	manifestDigest, manifestSize, err := writeLocalTemplateOCIManifest(repoDir, layerDigest, int64(len(layer)))
	if err != nil {
		return "", err
	}
	if err := upsertTaggedImageDescriptor(repoDir, helmChartTag(chart.Version), ociDescriptor{
		MediaType: ociManifestMediaType,
		Digest:    manifestDigest,
		Size:      manifestSize,
	}); err != nil {
		return "", err
	}
	if progress != nil {
		progress(DownloadEvent{Type: DownloadEventDone, Name: chart.Name, Current: int64(len(layer)), Total: int64(len(layer))})
	}
	return layerDigest, nil
}

// templateOCIReference builds the registry reference for a template chart.
func templateOCIReference(chart TemplateChart) (name.Reference, error) {
	ref := splitImageRefParts(stringsTrimOCIPrefix(chart.URL))
	repo := ref.repo
	if repo == "" {
		return nil, fmt.Errorf("template %s has invalid OCI URL %q", chart.Name, chart.URL)
	}
	value := repo
	if chart.Digest != "" {
		value += "@" + chart.Digest
	} else if ref.digest != "" {
		value += "@" + ref.digest
	} else {
		tag := ref.tag
		if tag == "" {
			tag = helmChartTag(chart.Version)
		}
		value += ":" + tag
	}
	return name.ParseReference(value, name.WeakValidation)
}

type imageRefParts struct {
	repo   string
	tag    string
	digest string
}

// splitImageRefParts converts splitImageRef's tuple into a named struct.
func splitImageRefParts(value string) imageRefParts {
	repo, tag, digest := splitImageRef(value)
	return imageRefParts{repo: repo, tag: tag, digest: digest}
}

// stringsTrimOCIPrefix removes an oci:// prefix if present.
func stringsTrimOCIPrefix(value string) string {
	if len(value) >= len("oci://") && value[:len("oci://")] == "oci://" {
		return value[len("oci://"):]
	}
	return value
}

// chartLayerFromDescriptor returns the Helm chart layer digest and size.
func chartLayerFromDescriptor(desc *remote.Descriptor) (string, int64, error) {
	var manifest struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(desc.Manifest, &manifest); err != nil {
		return "", 0, err
	}
	for _, layer := range manifest.Layers {
		if layer.MediaType == helmChartLayerMediaType {
			return layer.Digest, layer.Size, nil
		}
	}
	if len(manifest.Layers) == 1 {
		return manifest.Layers[0].Digest, manifest.Layers[0].Size, nil
	}
	return "", 0, fmt.Errorf("no Helm chart content layer found")
}

// writeLocalTemplateOCIManifest writes a minimal OCI manifest for a chart layer.
func writeLocalTemplateOCIManifest(repoDir, layerDigest string, layerSize int64) (string, int64, error) {
	config := []byte("{}")
	configDigest := digestBytes(config)
	if err := writeDigestBlob(repoDir, configDigest, config); err != nil {
		return "", 0, err
	}
	body := struct {
		SchemaVersion int             `json:"schemaVersion"`
		MediaType     string          `json:"mediaType"`
		Config        ociDescriptor   `json:"config"`
		Layers        []ociDescriptor `json:"layers"`
	}{
		SchemaVersion: 2,
		MediaType:     ociManifestMediaType,
		Config: ociDescriptor{
			MediaType: helmConfigMediaType,
			Digest:    configDigest,
			Size:      int64(len(config)),
		},
		Layers: []ociDescriptor{{
			MediaType: helmChartLayerMediaType,
			Digest:    layerDigest,
			Size:      layerSize,
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", 0, err
	}
	manifestDigest := digestBytes(raw)
	if err := writeDigestBlob(repoDir, manifestDigest, raw); err != nil {
		return "", 0, err
	}
	return manifestDigest, int64(len(raw)), nil
}

// ensureOCIRepo creates the repository-local OCI layout scaffolding.
func ensureOCIRepo(repoDir string) error {
	if err := os.MkdirAll(filepath.Join(repoDir, "blobs", "sha256"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoDir, "oci-layout"), ociLayoutJSON, 0o644)
}

// digestBytes returns the sha256 digest string for body.
func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
