// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package seeds

import (
	"context"
	"fmt"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/deps"
)

const (
	defaultDepsBaseURL = "https://deps.podplane.dev"

	Recommended = "recommended"
	Minimal     = "minimal"
	None        = "none"
)

type ResolveOptions struct {
	Context  context.Context
	Name     string
	Version  string
	BaseURL  string
	CacheDir string
}

type ResolveClusterOptions struct {
	Context           context.Context
	ClusterConfigPath string
	BaseURL           string
	CacheDir          string
}

type Seed struct {
	Name    string
	Version string
}

// ParseName returns a validated seed name, applying the default when value is empty.
func ParseName(value string) (string, error) {
	if value == "" {
		value = Recommended
	}
	switch value {
	case Recommended, Minimal, None:
		return value, nil
	default:
		return "", fmt.Errorf("invalid cluster.seed.name %q (must be %q, %q, or %q)", value, Recommended, Minimal, None)
	}
}

// ResolveSeedPath resolves a published seed name and version to a local seed file path.
func ResolveSeedPath(opts ResolveOptions) (string, error) {
	name, err := ParseName(opts.Name)
	if err != nil {
		return "", err
	}
	if name == None {
		return "", nil
	}
	if opts.Version == "" {
		return "", fmt.Errorf("cluster.seed.version is required when cluster.seed.name is %q", name)
	}
	if opts.CacheDir == "" {
		return "", fmt.Errorf("seed cache directory is required")
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultDepsBaseURL
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return deps.NewManager(baseURL, opts.CacheDir).EnsureSeedSnapshot(ctx, name, opts.Version, nil)
}

// ResolveClusterSeedPath resolves the seed configured in a cluster config to a local seed file path.
func ResolveClusterSeedPath(opts ResolveClusterOptions) (string, error) {
	seed, err := ReadClusterSeed(opts.ClusterConfigPath)
	if err != nil {
		return "", err
	}
	if seed.Name == None {
		return "", nil
	}
	return ResolveSeedPath(ResolveOptions{
		Context:  opts.Context,
		Name:     seed.Name,
		Version:  seed.Version,
		BaseURL:  opts.BaseURL,
		CacheDir: opts.CacheDir,
	})
}

// ReadClusterSeed returns the seed name and version configured in a cluster config.
func ReadClusterSeed(clusterConfigPath string) (Seed, error) {
	cluster, err := clusterconfig.Load(clusterConfigPath)
	if err != nil {
		return Seed{}, err
	}
	if cluster.Cluster.Seed.Name == "" {
		return Seed{Name: None}, nil
	}
	name, err := ParseName(cluster.Cluster.Seed.Name)
	if err != nil {
		return Seed{}, err
	}
	return Seed{Name: name, Version: cluster.Cluster.Seed.Version}, nil
}
