// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package seeds

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

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
	Digest   string
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
	Digest  string
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
	path, err := deps.NewManager(baseURL, opts.CacheDir).EnsureSeedSnapshot(ctx, name, opts.Version, nil)
	if err != nil {
		return "", err
	}
	if err := VerifySeedFile(path, opts.Digest); err != nil {
		return "", err
	}
	return path, nil
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
		Digest:   seed.Digest,
		BaseURL:  opts.BaseURL,
		CacheDir: opts.CacheDir,
	})
}

// ReadClusterSeed returns the seed identity configured in a cluster config.
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
	return Seed{Name: name, Version: cluster.Cluster.Seed.Version, Digest: cluster.Cluster.Seed.Digest}, nil
}

// VerifySeedFile verifies path against an expected SHA-512 digest.
func VerifySeedFile(path, expected string) error {
	if expected == "" {
		return fmt.Errorf("expected seed digest is required")
	}
	algorithm, encoded, ok := strings.Cut(expected, ":")
	if !ok || algorithm != "sha512" || len(encoded) != sha512.Size*2 {
		return fmt.Errorf("invalid expected seed digest %q", expected)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return fmt.Errorf("invalid expected seed digest %q: %w", expected, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Podplane seed file for verification: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha512.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash Podplane seed file: %w", err)
	}
	actual := "sha512:" + hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("podplane seed file digest is %s, want %s", actual, expected)
	}
	return nil
}
