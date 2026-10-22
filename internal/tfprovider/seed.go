// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/nstance-dev/nstance/v2/pkg/nonce"

	"github.com/podplane/podplane/internal/components"
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

// createSeed adopts completed snapshots before accessing keys. For new clusters
// it renders configuration, prepares component prerequisites, and only then
// publishes the snapshot. Rendering itself never provisions external state.
func createSeed(ctx context.Context, opts SeedOptions, store seedSnapshotStore, openKeyStore func(context.Context, cloudSecretOptions) (cloudSecretStore, error)) (seedSnapshotMetadata, error) {
	meta, adopted, err := completedSeed(ctx, opts, store)
	if err != nil || adopted {
		return meta, err
	}
	if err := store.RequireEmpty(ctx); err != nil {
		if winner, ok, readErr := completedSeed(ctx, opts, store); readErr == nil && ok {
			return winner, nil
		}
		return meta, err
	}
	if err := validateBootstrapScope(opts); err != nil {
		return meta, err
	}
	seedPath, cleanup, err := resolveSeedPath(ctx, opts)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return meta, err
	}
	prepared, err := netsyseed.Prepare(netsyseed.SnapshotOptions{
		Context:           ctx,
		ClusterConfigPath: opts.ClusterConfigPath,
		SeedPath:          seedPath,
		ValuesContent:     []byte(opts.ValuesContent),
		ValuesFile:        opts.ValuesFile,
	})
	if err != nil {
		return meta, err
	}
	cfg, err := components.ParseValues(prepared.ComponentValues)
	if err != nil {
		return meta, err
	}
	required, err := cfg.RequiredSecrets(nil)
	if err != nil {
		return meta, err
	}
	var data bytes.Buffer
	if err := prepared.WriteSnapshot(&data); err != nil {
		clear(data.Bytes())
		return meta, err
	}
	snapshot := data.Bytes()
	defer func() { clear(snapshot) }()
	if b := opts.NstanceBootstrap; b != nil {
		// Validate overlay and object collisions before reading the signing key.
		probe, err := addOperatorBootstrap(snapshot, b, nil, "")
		clear(probe)
		if err != nil {
			return meta, err
		}
		waitCtx, cancel := context.WithTimeout(ctx, publicCAWait)
		ca, err := waitPublicCA(waitCtx, func(ctx context.Context) ([]byte, error) { return store.PublicCA(ctx, b) }, 2*time.Second)
		cancel()
		if err != nil {
			return meta, err
		}
		// Another apply may have completed while the CA was being initialized.
		meta, adopted, err = completedSeed(ctx, opts, store)
		if err != nil || adopted {
			return meta, err
		}
		secret, _ := b.secret(opts)
		keyStore, err := openKeyStore(ctx, secret)
		if err != nil {
			return meta, errors.New("cannot configure Nstance signing-key backend")
		}
		defer func() { _ = keyStore.Close() }()
		current, err := keyStore.Metadata(ctx)
		if err != nil || current.Identity != b.SigningKeyID.ValueString() || current.Version != b.SigningKeyVersion.ValueString() {
			return meta, errors.New("recorded Nstance signing-key identity or version changed; restore the original key")
		}
		key, err := cloudSecretValue(ctx, keyStore, b.SigningKeyVersion.ValueString())
		if err != nil {
			return meta, err
		}
		defer clear(key)
		fingerprint, err := nonce.FingerprintSigningKey(key)
		if err != nil || fingerprint != b.SigningKeyFingerprint.ValueString() {
			return meta, errors.New("recorded Nstance signing-key fingerprint mismatch; restore the original key")
		}
		token, err := nonce.GenerateOperatorToken(key, b.ClusterID.ValueString(), b.Tenant.ValueString(), operatorNonceTTL)
		if err != nil {
			return meta, errors.New("cannot generate Nstance operator registration nonce")
		}
		injected, err := addOperatorBootstrap(snapshot, b, ca, token)
		if err != nil {
			return meta, err
		}
		clear(snapshot)
		snapshot = injected
	}
	if err := store.RequireEmpty(ctx); err != nil {
		if winner, ok, readErr := completedSeed(ctx, opts, store); readErr == nil && ok {
			return winner, nil
		}
		return meta, err
	}
	for _, secret := range required {
		if err := ensureComponentSecret(ctx, opts, secret, openKeyStore); err != nil {
			return meta, err
		}
	}
	// Publish only after both snapshot construction and secret provisioning succeed.
	if err := store.Upload(ctx, snapshot, seedCompletion(opts)); err != nil {
		// Recover a concurrent winner or an upload whose response was lost.
		if winner, ok, readErr := completedSeed(ctx, opts, store); readErr == nil && ok {
			return winner, nil
		}
		return meta, errors.New("cannot upload create-only Netsy snapshot; retry will adopt a completed upload, never overwrite live state")
	}
	meta, adopted, err = completedSeed(ctx, opts, store)
	if err == nil && !adopted {
		err = errors.New("uploaded seed metadata is not yet available; retry to adopt")
	}
	return meta, err
}
