// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"path/filepath"
	"testing"
)

func TestCacheDomainPaths(t *testing.T) {
	manager := NewManager("https://example.invalid/deps", "/cache/deps")
	dep := Dependency{
		Version: "1.2.3",
		URL:     "https://example.invalid/artifacts/runc.arm64",
	}

	if got, want := manager.VMConfigCacheDir(), filepath.Join("/cache/deps", "vmconfig"); got != want {
		t.Fatalf("VMConfigCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.VMConfigManifestCacheDir(), filepath.Join("/cache/deps", "vmconfig", "manifests"); got != want {
		t.Fatalf("VMConfigManifestCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.VMConfigArtifactsCacheDir(), filepath.Join("/cache/deps", "vmconfig", "artifacts"); got != want {
		t.Fatalf("VMConfigArtifactsCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.VMConfigManifestCachePath(DefaultKind, "arm64"), filepath.Join("/cache/deps", "vmconfig", "manifests", "vmconfig_knc_debian-13_arm64.json"); got != want {
		t.Fatalf("VMConfigManifestCachePath() = %q, want %q", got, want)
	}
	if got, want := manager.VMConfigArtifactCachePath("runc", dep), filepath.Join("/cache/deps", "vmconfig", "artifacts", "runc", "1.2.3", "runc.arm64"); got != want {
		t.Fatalf("VMConfigArtifactCachePath() = %q, want %q", got, want)
	}
	if got, want := manager.ComponentsCacheDir(), filepath.Join("/cache/deps", "components"); got != want {
		t.Fatalf("ComponentsCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.ComponentsManifestCacheDir(), filepath.Join("/cache/deps", "components", "manifests"); got != want {
		t.Fatalf("ComponentsManifestCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.RegistryCacheDir(), filepath.Join("/cache", "registry"); got != want {
		t.Fatalf("RegistryCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.ComponentsManifestCachePath(), filepath.Join("/cache/deps", "components", "manifests", "components.json"); got != want {
		t.Fatalf("ComponentsManifestCachePath() = %q, want %q", got, want)
	}
	if got, want := manager.TemplatesCacheDir(), filepath.Join("/cache/deps", "templates"); got != want {
		t.Fatalf("TemplatesCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.TemplatesManifestCacheDir(), filepath.Join("/cache/deps", "templates", "manifests"); got != want {
		t.Fatalf("TemplatesManifestCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.TemplatesChartsCacheDir(), filepath.Join("/cache/deps", "templates", "charts"); got != want {
		t.Fatalf("TemplatesChartsCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.TemplatesManifestCachePath(), filepath.Join("/cache/deps", "templates", "manifests", "templates.json"); got != want {
		t.Fatalf("TemplatesManifestCachePath() = %q, want %q", got, want)
	}
	if got, want := manager.SeedsCacheDir(), filepath.Join("/cache/deps", "seeds"); got != want {
		t.Fatalf("SeedsCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.SeedsManifestCacheDir(), filepath.Join("/cache/deps", "seeds", "manifests"); got != want {
		t.Fatalf("SeedsManifestCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.SeedsSnapshotsCacheDir(), filepath.Join("/cache/deps", "seeds", "snapshots"); got != want {
		t.Fatalf("SeedsSnapshotsCacheDir() = %q, want %q", got, want)
	}
	if got, want := manager.SeedsManifestCachePath(), filepath.Join("/cache/deps", "seeds", "manifests", "seeds.json"); got != want {
		t.Fatalf("SeedsManifestCachePath() = %q, want %q", got, want)
	}
	if got, want := manager.SeedSnapshotCachePath("recommended", "1.2.3-1", "recommended.netsy"), filepath.Join("/cache/deps", "seeds", "snapshots", "recommended", "1.2.3-1", "recommended.netsy"); got != want {
		t.Fatalf("SeedSnapshotCachePath() = %q, want %q", got, want)
	}
}

func TestRegistryCacheDirWithCustomDepsCacheDir(t *testing.T) {
	manager := NewManager("https://example.invalid/deps", "/custom/podplane-deps")
	if got, want := manager.RegistryCacheDir(), filepath.Join("/custom/podplane-deps", "registry"); got != want {
		t.Fatalf("RegistryCacheDir() = %q, want %q", got, want)
	}
}

func TestManifestFilename(t *testing.T) {
	// The filename is part of the local cache path contract.
	tests := []struct {
		name string
		kind string
		arch string
		want string
	}{
		{
			name: "knc arm64",
			kind: "knc",
			arch: "arm64",
			want: "vmconfig_knc_debian-13_arm64.json",
		},
		{
			name: "knc amd64",
			kind: "knc",
			arch: "amd64",
			want: "vmconfig_knc_debian-13_amd64.json",
		},
		{
			name: "default kind constant",
			kind: DefaultKind,
			arch: "arm64",
			want: "vmconfig_knc_debian-13_arm64.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := manifestFilename(tt.kind, tt.arch)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
