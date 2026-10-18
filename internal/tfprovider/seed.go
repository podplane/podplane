// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/podplane/podplane/pkg/netsyseed"
	"github.com/podplane/podplane/pkg/seeds"
)

const netsyBootstrapObject = "bootstrap.netsy"

// SeedOptions contains the inputs used to create and upload a Netsy seed snapshot.
type SeedOptions struct {
	ClusterConfigPath string
	SeedPath          string
	ValuesFile        string
	ValuesContent     string
	Bucket            string
	Prefix            string
	Region            string
	Profile           string
	Project           string
	NstanceBootstrap  *nstanceBootstrapModel
}

// objectKey returns the Netsy bootstrap object key under the configured prefix.
func (opts SeedOptions) objectKey() string {
	return path.Join(opts.objectPrefix(), netsyBootstrapObject)
}

// objectPrefix returns the normalized object prefix used for Netsy state checks.
func (opts SeedOptions) objectPrefix() string {
	return strings.Trim(opts.Prefix, "/")
}

// resolveSeedPath returns the explicit or downloaded Podplane seed file path.
func resolveSeedPath(ctx context.Context, opts SeedOptions) (seedPath string, cleanup func(), err error) {
	if opts.SeedPath != "" {
		seed, err := seeds.ReadClusterSeed(opts.ClusterConfigPath)
		if err != nil {
			return "", nil, err
		}
		return resolveExplicitSeedPath(ctx, opts.SeedPath, seed.Digest)
	}
	depsCacheDir, err := os.MkdirTemp("", "podplane-deps-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary deps cache: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(depsCacheDir) }
	seedPath, err = seeds.ResolveClusterSeedPath(seeds.ResolveClusterOptions{
		Context:           ctx,
		ClusterConfigPath: opts.ClusterConfigPath,
		CacheDir:          depsCacheDir,
	})
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return seedPath, cleanup, nil
}

// resolveExplicitSeedPath materializes an explicit HTTP(S) seed when needed
// and verifies it against the digest pinned in cluster config.
func resolveExplicitSeedPath(ctx context.Context, source, digest string) (seedPath string, cleanup func(), err error) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		if err := seeds.VerifySeedFile(source, digest); err != nil {
			return "", nil, err
		}
		return source, nil, nil
	}
	tmp, err := os.CreateTemp("", "podplane-seed-*.netsy")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary seed file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup = func() { _ = os.Remove(tmpPath) }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return "", nil, fmt.Errorf("create seed download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return "", nil, fmt.Errorf("download Podplane seed file %s: %w", source, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = tmp.Close()
		cleanup()
		return "", nil, fmt.Errorf("download Podplane seed file %s: HTTP %s", source, resp.Status)
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", nil, fmt.Errorf("write temporary seed file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temporary seed file: %w", err)
	}
	if err := seeds.VerifySeedFile(tmpPath, digest); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmpPath, cleanup, nil
}

// generateNetsySeedSnapshot keeps cluster-specific snapshots in memory.
func generateNetsySeedSnapshot(ctx context.Context, opts SeedOptions, seedPath string) ([]byte, error) {
	var snapshot bytes.Buffer
	if err := netsyseed.WriteSnapshot(&snapshot, netsyseed.SnapshotOptions{
		Context:           ctx,
		ClusterConfigPath: opts.ClusterConfigPath,
		SeedPath:          seedPath,
		ValuesContent:     []byte(opts.ValuesContent),
		ValuesFile:        opts.ValuesFile,
	}); err != nil {
		clear(snapshot.Bytes())
		return nil, err
	}
	return snapshot.Bytes(), nil
}
