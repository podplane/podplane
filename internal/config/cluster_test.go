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

// TestSetClusterSummary checks summary round-tripping and flattened persistence.
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

// TestClusterSummaryFromConfigStripsSecretsProviderDetails checks that only provider selection and kind are retained.
func TestClusterSummaryFromConfigStripsSecretsProviderDetails(t *testing.T) {
	cluster := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "test-cluster",
		Secrets: clusterconfig.Secrets{
			DefaultProvider: "aws-secrets-manager",
			Providers: map[string]clusterconfig.SecretsProvider{
				"aws-secrets-manager": {
					Kind:       "aws",
					KeyPrefix:  "shared-secrets",
					ObjectType: "secretsmanager",
					Region:     "us-east-1",
				},
			},
		},
	}}

	summary := ClusterSummaryFromConfig(cluster)
	provider := summary.Secrets.Providers["aws-secrets-manager"]
	if got, want := summary.Secrets.DefaultProvider, "aws-secrets-manager"; got != want {
		t.Fatalf("default provider = %q, want %q", got, want)
	}
	if got, want := provider.Kind, "aws"; got != want {
		t.Fatalf("provider kind = %q, want %q", got, want)
	}
	if provider.KeyPrefix != "" || provider.ObjectType != "" || provider.Region != "" || provider.ProjectID != "" || provider.Location != "" || provider.Address != "" || provider.MountPath != "" {
		t.Fatalf("provider details persisted in summary: %#v", provider)
	}
}

// TestSetLocalClusterSummaryUsesScopedKey checks that local summaries do not collide with remote entries.
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

// TestClusterSummaryMissing checks that an unknown cluster returns an empty summary without error.
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
