// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

func TestSetClusterSummary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	summary := ClusterSummary{
		ID:   "test-cluster",
		Name: "Test Cluster",
		OIDC: clusterconfig.OIDC{IssuerURL: "https://auth.example.com", ClientID: "test-client"},
		Kubernetes: clusterconfig.Kubernetes{
			APIHostname: "api.example.com",
			APIPort:     6443,
		},
		Components: ClusterSummaryClusterComponents{
			Registry: &clusterconfig.ComponentsRegistry{
				Mirror: clusterconfig.ComponentsRegistryMirror{Enabled: true, Hostname: "zot.local"},
			},
		},
	}
	if err := c.SetClusterSummary(summary, false); err != nil {
		t.Fatal(err)
	}
	got, err := c.ClusterSummary("test-cluster", false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, summary) {
		t.Fatalf("ClusterSummary() = %#v, want %#v", got, summary)
	}

	raw, err := os.ReadFile(filepath.Join(c.ConfigDirectory(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	clusters := file["clusters"].(map[string]any)
	entry := clusters["test-cluster"].(map[string]any)
	if _, ok := entry["cluster"]; ok {
		t.Fatalf("cluster summary should be flattened: %#v", entry)
	}
}

func TestSetLocalClusterSummaryUsesScopedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	summary := ClusterSummary{ID: "default", Name: "local-default"}
	if err := c.SetClusterSummary(summary, true); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ClusterSummary("default", true); err != nil || got.ID != "default" {
		t.Fatalf("ClusterSummary(local) = %#v, %v", got, err)
	}
	if got, err := c.ClusterSummary("default", false); err != nil || got.ID != "" {
		t.Fatalf("ClusterSummary(remote) = %#v, %v", got, err)
	}
}

func TestClusterSummaryMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.ClusterSummary("missing", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" {
		t.Fatalf("ClusterSummary() = %#v, want missing summary", got)
	}
}
