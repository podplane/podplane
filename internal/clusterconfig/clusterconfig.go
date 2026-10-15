// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tailscale/hujson"
)

// Load reads a .cluster.jsonc file from disk, strips comments, and decodes
// it into a ClusterConfig, rejecting unknown fields.
func Load(path string) (*ClusterConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cluster config %s: %w", path, err)
	}
	standardized, err := hujson.Standardize(raw)
	if err != nil {
		return nil, fmt.Errorf("parse cluster config %s: %w", path, err)
	}
	cfg := &ClusterConfig{}
	decoder := json.NewDecoder(bytes.NewReader(standardized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse cluster config %s: %w", path, err)
	}
	if cfg.Cluster.ID == "" {
		return nil, fmt.Errorf("cluster config %s: missing cluster.id", path)
	}
	if err := ValidateClusterID(cfg.Cluster.ID); err != nil {
		return nil, fmt.Errorf("cluster config %s: invalid cluster.id: %w", path, err)
	}
	if cfg.Cluster.OIDC.IssuerURL == "" {
		return nil, fmt.Errorf("cluster config %s: missing cluster.oidc.issuer_url", path)
	}
	if err := ValidateSeed(cfg.Cluster.Seed); err != nil {
		return nil, fmt.Errorf("cluster config %s: invalid cluster.seed: %w", path, err)
	}
	if err := ValidateComponents(cfg.Cluster.Components); err != nil {
		return nil, fmt.Errorf("cluster config %s: invalid cluster.components: %w", path, err)
	}
	return cfg, nil
}

// Write writes a formatted cluster configuration file to disk.
func Write(path string, cfg *ClusterConfig) error {
	out := *cfg
	if out.Schema == "" {
		out.Schema = DefaultSchemaRef
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cluster config: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write cluster config %s: %w", path, err)
	}
	return nil
}
