// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/podplane/podplane/internal/config"
)

const validClusterConfigJSON = `{
  "cluster": {
    "id": "test-cluster",
    "oidc": { "issuer_url": "https://auth.example.com" },
    "pools": {
      "control-plane": { "arch": "arm64", "instance_type": "t4g.medium", "size": 1 }
    },
    "providers": [{
      "kind": "aws",
      "region": "us-east-1",
      "account": "123456789012",
      "vpc": { "v4cidr": "172.18.0.0/16", "v6cidr": "auto" },
      "zones": {
        "us-east-1a": [
          { "v4cidr": "172.18.10.0/28", "services": ["nat", "nlb"], "public": true },
          { "v4cidr": "172.18.20.0/28", "services": ["nstance"] },
          { "v4cidr": "172.18.1.0/24", "pool": "control-plane" }
        ]
      },
      "load_balancer": {
        "public": true,
        "listeners": [{ "port": 6443, "pool": "control-plane" }]
      }
    }],
    "kubernetes": {
      "cluster_cidr": ["100.64.0.0/10"],
      "service_cidr": ["198.18.0.0/15"]
    }
  }
}`

// TestClusterCreateNoApplyGeneratesTerraform verifies cluster create writes
// managed Terraform without invoking OpenTofu/Terraform.
func TestClusterCreateNoApplyGeneratesTerraform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.cluster.jsonc")
	if err := os.WriteFile(path, []byte(validClusterConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newClusterCreateCmd(&config.Config{})
	cmd.SetArgs([]string{"--cluster-config", path, "--no-apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cluster create --no-apply returned error: %v", err)
	}
	for _, name := range []string{
		"podplane.cluster.main.tf",
		"podplane.cluster.variables.tf",
		"podplane.cluster.outputs.tf",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("AWS cluster tf %s was not generated: %v", name, err)
		}
	}
}

// TestClusterDeleteNoApplyValidatesOnly verifies cluster delete no-apply
// validates the config without invoking destroy dependencies.
func TestClusterDeleteNoApplyValidatesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.cluster.jsonc")
	if err := os.WriteFile(path, []byte(validClusterConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newClusterDeleteCmd(&config.Config{})
	cmd.SetArgs([]string{"--cluster-config", path, "--no-apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cluster delete --no-apply returned error: %v", err)
	}
}
