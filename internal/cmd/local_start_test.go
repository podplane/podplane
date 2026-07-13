// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/local"
	"github.com/podplane/podplane/pkg/seeds"
)

// TestLocalStartSeedNameUsesExistingClusterSeed verifies the local-start
// TUI plans checks from the existing cluster config on restart. The
// --components flag only applies to first boot, so a minimal existing cluster
// must not get recommended-only progress rows such as trust-manager.
func TestLocalStartSeedNameUsesExistingClusterSeed(t *testing.T) {
	const existingMinimalClusterConfig = `
{
  "cluster": {
    "id": "default",
    "oidc": { "issuer_url": "https://oidc.localhost:4433/oidc" },
    "seed": { "name": "minimal", "version": "v1", "digest": "sha512:00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000" }
  }
}`

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	oldLocalClusterID := localClusterID
	localClusterID = "default"
	t.Cleanup(func() { localClusterID = oldLocalClusterID })
	c := &config.Config{}
	path := local.ClusterConfigPath(c.DataDirectory(), localClusterID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(existingMinimalClusterConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := localStartSeedName(local.NewManager(c, localClusterID), true, seeds.Recommended)
	if err != nil {
		t.Fatalf("localStartSeedName returned error: %v", err)
	}
	if got != seeds.Minimal {
		t.Fatalf("localStartSeedName = %q, want %q", got, seeds.Minimal)
	}
}
