// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// ClusterSummary is the subset of podplane.cluster.jsonc cached for commands
// that run from kube context instead of a cluster config file.
type ClusterSummary struct {
	ID         string                          `mapstructure:"id" json:"id"`
	Name       string                          `mapstructure:"name" json:"name"`
	OIDC       clusterconfig.OIDC              `mapstructure:"oidc" json:"oidc"`
	Kubernetes clusterconfig.Kubernetes        `mapstructure:"kubernetes" json:"kubernetes"`
	Components ClusterSummaryClusterComponents `mapstructure:"components" json:"components,omitempty"`
}

// ClusterSummaryClusterComponents is the subset of clusterconfig.Components
// persisted in the CLI config file.
type ClusterSummaryClusterComponents struct {
	Registry *clusterconfig.ComponentsRegistry `mapstructure:"registry" json:"registry,omitempty"`
}

// ClusterSummaryFromConfig extracts the cached cluster summary from a full
// cluster config.
func ClusterSummaryFromConfig(cluster *clusterconfig.ClusterConfig) ClusterSummary {
	return ClusterSummary{
		ID:         cluster.Cluster.ID,
		Name:       cluster.Cluster.Name,
		OIDC:       cluster.Cluster.OIDC,
		Kubernetes: cluster.Cluster.Kubernetes,
		Components: ClusterSummaryClusterComponents{
			Registry: cluster.Cluster.Components.Registry,
		},
	}
}

// clusterSummaryKey returns the config map key for a cached cluster summary.
func clusterSummaryKey(clusterID string, local bool) string {
	if local {
		return "local:" + clusterID
	}
	return clusterID
}

// ClusterSummary returns the cached cluster summary for clusterID. Missing
// entries return a zero-value ClusterSummary and no error.
func (c *Config) ClusterSummary(clusterID string, local bool) (ClusterSummary, error) {
	var summary ClusterSummary
	if clusterID == "" {
		return summary, fmt.Errorf("ClusterSummary: cluster_id is required")
	}
	key := clusterSummaryKey(clusterID, local)
	if raw := c.viperFile.GetStringMap("clusters." + key); len(raw) > 0 {
		if err := decodeMap(raw, &summary); err != nil {
			return ClusterSummary{}, fmt.Errorf("decode cluster summary for %s: %w", clusterID, err)
		}
	}
	return summary, nil
}

// SetClusterSummary writes the cached cluster summary.
func (c *Config) SetClusterSummary(summary ClusterSummary, local bool) error {
	if summary.ID == "" {
		return fmt.Errorf("SetClusterSummary: cluster.id is required")
	}
	cluster := map[string]any{
		"id":         summary.ID,
		"name":       summary.Name,
		"oidc":       summary.OIDC,
		"kubernetes": summary.Kubernetes,
	}
	if summary.Components.Registry != nil {
		cluster["components"] = map[string]any{
			"registry": summary.Components.Registry,
		}
	}
	c.viperFile.Set("clusters."+clusterSummaryKey(summary.ID, local), cluster)
	if err := c.SaveFile(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
