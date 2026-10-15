// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPopulateSeedsCacheCopiesLocalManifestSnapshots verifies manifest-relative snapshots are copied and marked cached.
func TestPopulateSeedsCacheCopiesLocalManifestSnapshots(t *testing.T) {
	dir := t.TempDir()
	manifestDir := filepath.Join(dir, "manifests")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	recommended := filepath.Join(dir, "recommended.netsy")
	minimal := filepath.Join(dir, "minimal.netsy")
	if err := os.WriteFile(recommended, []byte("recommended"), 0o644); err != nil {
		t.Fatalf("write recommended: %v", err)
	}
	if err := os.WriteFile(minimal, []byte("minimal"), 0o644); err != nil {
		t.Fatalf("write minimal: %v", err)
	}
	manifest := SeedsManifest{Seeds: Seeds{
		Version:    "dev",
		Components: "dev",
		Snapshots: map[string]SeedSnapshot{
			"recommended": {Path: "../recommended.netsy"},
			"minimal":     {Path: "../minimal.netsy"},
		},
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestPath := filepath.Join(manifestDir, "seeds.json")
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	manager := NewManager("https://example.invalid/deps", filepath.Join(dir, "cache"))
	loaded, err := readSeedsManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := manager.populateSeedsCache(context.Background(), loaded, manifestPath, nil, nil); err != nil {
		t.Fatalf("populateSeedsCache: %v", err)
	}
	for _, name := range []string{"recommended", "minimal"} {
		path, err := manager.CachedSeedSnapshotPath(name, "")
		if err == nil {
			t.Fatalf("CachedSeedSnapshotPath(%q) succeeded before manifest write: %s", name, path)
		}
		if !loaded.Seeds.Snapshots[name].Cached {
			t.Fatalf("snapshot %q was not marked cached", name)
		}
		if _, err := os.Stat(manager.SeedSnapshotCachePath(name, "dev", name+".netsy")); err != nil {
			t.Fatalf("cached snapshot %q missing: %v", name, err)
		}
	}
}

// TestValidateSeedSnapshotRequiresPathOrURL verifies snapshots need exactly one local or remote source.
func TestValidateSeedSnapshotRequiresPathOrURL(t *testing.T) {
	if err := validateSeedSnapshot("recommended", SeedSnapshot{Path: "recommended.netsy"}); err != nil {
		t.Fatalf("validate path snapshot: %v", err)
	}
	if err := validateSeedSnapshot("recommended", SeedSnapshot{URL: "https://example.invalid/recommended.netsy", Digest: "sha512:" + strings.Repeat("a", 128), Size: 1}); err != nil {
		t.Fatalf("validate url snapshot: %v", err)
	}
	if err := validateSeedSnapshot("recommended", SeedSnapshot{}); err == nil {
		t.Fatal("validate without path or url returned nil error")
	}
	if err := validateSeedSnapshot("recommended", SeedSnapshot{Path: "recommended.netsy", URL: "https://example.invalid/recommended.netsy"}); err == nil {
		t.Fatal("validate with path and url returned nil error")
	}
}
