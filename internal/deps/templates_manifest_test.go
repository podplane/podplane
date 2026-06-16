// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import "testing"

func TestTemplateManifestDownloadImageIndexesFiltersByArch(t *testing.T) {
	manifest := &TemplatesManifest{Templates: Templates{Images: []TemplateImage{
		{Image: "amd64", Platform: "linux/amd64"},
		{Image: "arm64", Platform: "linux/arm64/v8"},
		{Image: "all"},
	}}}

	indexes := manifest.DownloadImageIndexes([]string{"arm64"})
	if got, want := templateImageNamesAt(manifest.Templates.Images, indexes), []string{"arm64", "all"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("filtered images = %v, want %v", got, want)
	}
}

func TestTemplateManifestResetAndMarkImageCached(t *testing.T) {
	manifest := &TemplatesManifest{Templates: Templates{Images: []TemplateImage{{Image: "caddy", Cached: true}}}}
	manifest.ResetCached()
	if manifest.Templates.Images[0].Cached {
		t.Fatal("ResetCached left template image cached")
	}
	manifest.MarkImageCached(0)
	if !manifest.Templates.Images[0].Cached {
		t.Fatal("MarkImageCached did not mark template image cached")
	}
}

func templateImageNamesAt(images []TemplateImage, indexes []int) []string {
	names := make([]string, 0, len(indexes))
	for _, index := range indexes {
		names = append(names, images[index].Image)
	}
	return names
}
