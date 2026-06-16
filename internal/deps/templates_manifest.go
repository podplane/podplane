// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import "strings"

const (
	TemplateChartTypeOCI   = "oci"
	TemplateChartTypeChart = "chart"
)

// TemplatesManifest is the top-level templates dependency manifest.
type TemplatesManifest struct {
	Templates Templates `json:"templates"`
}

type Templates struct {
	Version string          `json:"version"`
	Charts  []TemplateChart `json:"charts"`
	Images  []TemplateImage `json:"images"`
}

type TemplateChart struct {
	Name             string               `json:"name"`
	Version          string               `json:"version"`
	Type             string               `json:"type"`
	URL              string               `json:"url,omitempty"`
	Path             string               `json:"path,omitempty"`
	Digest           string               `json:"digest,omitempty"`
	Dependencies     TemplateDependencies `json:"dependencies,omitempty"`
	Cached           bool                 `json:"cached,omitempty"`
	ChartLayerDigest string               `json:"chartLayerDigest,omitempty"`
}

type TemplateDependencies struct {
	Components []string `json:"components,omitempty"`
}

type TemplateImage struct {
	Image     string            `json:"image"`
	Digest    string            `json:"digest"`
	Size      int64             `json:"size"`
	Platform  string            `json:"platform,omitempty"`
	Index     string            `json:"index,omitempty"`
	Templates map[string]string `json:"templates,omitempty"`
	Cached    bool              `json:"cached,omitempty"`
}

// ResetCached clears local cache-state markers from template charts.
func (m *TemplatesManifest) ResetCached() {
	if m == nil {
		return
	}
	for i := range m.Templates.Charts {
		m.Templates.Charts[i].Cached = false
		m.Templates.Charts[i].ChartLayerDigest = ""
	}
	for i := range m.Templates.Images {
		m.Templates.Images[i].Cached = false
	}
}

// MarkCached marks a template chart as cached with its chart layer digest.
func (m *TemplatesManifest) MarkCached(index int, chartLayerDigest string) {
	if m == nil || index < 0 || index >= len(m.Templates.Charts) {
		return
	}
	m.Templates.Charts[index].Cached = true
	m.Templates.Charts[index].ChartLayerDigest = chartLayerDigest
}

// MarkImageCached marks one template image entry as present in the local cache.
func (m *TemplatesManifest) MarkImageCached(index int) {
	if m == nil || index < 0 || index >= len(m.Templates.Images) {
		return
	}
	m.Templates.Images[index].Cached = true
}

// DownloadImageIndexes returns template image indexes matching the given archs.
func (m *TemplatesManifest) DownloadImageIndexes(archs []string) []int {
	wantedArchs := map[string]bool{}
	for _, arch := range archs {
		wantedArchs[arch] = true
	}
	indexes := make([]int, 0, len(m.Templates.Images))
	for i, image := range m.Templates.Images {
		if image.Platform != "" && !wantedArchs[archFromPlatform(image.Platform)] {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

// templateComponentImage adapts a template image manifest entry to the shared
// image mirror cache writer used for component images.
func templateComponentImage(image TemplateImage) ComponentImage {
	return ComponentImage{
		Component: "template",
		Image:     image.Image,
		Digest:    image.Digest,
		Size:      image.Size,
		Platform:  image.Platform,
		Index:     image.Index,
	}
}

// helmChartTag returns the OCI tag form Helm uses for a chart version.
func helmChartTag(version string) string {
	return strings.ReplaceAll(version, "+", "_")
}
