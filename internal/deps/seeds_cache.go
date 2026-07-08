// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/podplane/podplane/internal/filecache"
)

// SeedsCacheDir returns the seeds dependency-domain cache directory.
func (m *Manager) SeedsCacheDir() string {
	return filepath.Join(m.depsCacheDir, "seeds")
}

// SeedsManifestCacheDir returns the seeds manifest cache directory.
func (m *Manager) SeedsManifestCacheDir() string {
	return filepath.Join(m.SeedsCacheDir(), "manifests")
}

// SeedsSnapshotsCacheDir returns the cached seed snapshot directory.
func (m *Manager) SeedsSnapshotsCacheDir() string {
	return filepath.Join(m.SeedsCacheDir(), "snapshots")
}

// SeedsManifestCachePath returns the latest cached seeds manifest path.
func (m *Manager) SeedsManifestCachePath() string {
	return filepath.Join(m.SeedsManifestCacheDir(), "seeds.json")
}

// SeedSnapshotCachePath returns the cache path for a named seed snapshot.
func (m *Manager) SeedSnapshotCachePath(name, version, filename string) string {
	if filename == "" {
		filename = name + ".netsy"
	}
	return filepath.Join(m.SeedsSnapshotsCacheDir(), name, version, filename)
}

// WriteCachedSeedsManifest writes historical and latest seeds manifests.
func (m *Manager) WriteCachedSeedsManifest(raw []byte) error {
	historicalPath := filepath.Join(m.SeedsManifestCacheDir(), historicalManifestFilename("seeds", raw))
	if err := os.MkdirAll(filepath.Dir(historicalPath), 0o755); err != nil {
		return fmt.Errorf("failed to create seeds manifest cache directory: %w", err)
	}
	if err := os.WriteFile(historicalPath, raw, 0o644); err != nil {
		return fmt.Errorf("failed to write historical seeds manifest: %w", err)
	}
	if err := os.WriteFile(m.SeedsManifestCachePath(), raw, 0o644); err != nil {
		return fmt.Errorf("failed to write seeds manifest: %w", err)
	}
	return nil
}

// ReadCachedSeedsManifest reads the latest cached seeds manifest.
func (m *Manager) ReadCachedSeedsManifest() (*SeedsManifest, []byte, error) {
	raw, err := os.ReadFile(m.SeedsManifestCachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to read cached seeds manifest: %w", err)
	}
	var manifest SeedsManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, raw, fmt.Errorf("failed to parse cached seeds manifest: %w", err)
	}
	return &manifest, raw, nil
}

// EnsureSeedsManifestCached returns the cached seeds manifest, fetching and
// caching it first when the manifest is not already present locally.
func (m *Manager) EnsureSeedsManifestCached(ctx context.Context) (*SeedsManifest, error) {
	manifest, _, err := m.ReadCachedSeedsManifest()
	if err != nil {
		return nil, err
	}
	if manifest != nil {
		return manifest, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manifest, err = m.fetchSeedsManifest(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load seeds manifest: %w", err)
	}
	manifest.ResetCached()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode cached seeds manifest: %w", err)
	}
	if err := m.WriteCachedSeedsManifest(append(raw, '\n')); err != nil {
		return nil, err
	}
	return manifest, nil
}

// EnsureSeedSnapshotsCached returns the cached seeds manifest after ensuring
// all seed snapshots referenced by it are present in the local cache.
func (m *Manager) EnsureSeedSnapshotsCached(ctx context.Context) (*SeedsManifest, error) {
	manifest, err := m.EnsureSeedsManifestCached(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.cacheSeedSnapshots(ctx, manifest, "", nil, nil); err != nil {
		return nil, err
	}
	return manifest, nil
}

// CachedSeedsVersion returns the version in the latest cached seeds manifest.
func (m *Manager) CachedSeedsVersion() (string, error) {
	manifest, _, err := m.ReadCachedSeedsManifest()
	if err != nil {
		return "", err
	}
	if manifest == nil {
		return "", fmt.Errorf("seeds manifest is not cached; run `podplane deps download`")
	}
	return manifest.Seeds.Version, nil
}

// CachedSeedSnapshotPath returns the cached .netsy path for name.
func (m *Manager) CachedSeedSnapshotPath(name, version string) (string, error) {
	manifest, _, err := m.ReadCachedSeedsManifest()
	if err != nil {
		return "", err
	}
	if manifest == nil {
		return "", fmt.Errorf("seeds manifest is not cached; run `podplane deps download`")
	}
	if version != "" && manifest.Seeds.Version != version {
		return "", fmt.Errorf("cached seeds manifest version is %q, want %q; run `podplane deps download`", manifest.Seeds.Version, version)
	}
	snapshot, ok := manifest.Seeds.Snapshots[name]
	if !ok {
		return "", fmt.Errorf("seed snapshot %q was not found in cached seeds manifest", name)
	}
	if !snapshot.Cached {
		return "", fmt.Errorf("seed snapshot %q is not cached; run `podplane deps download`", name)
	}
	path := m.seedSnapshotCachePath(manifest, name, snapshot)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("seed snapshot %q is missing from cache; run `podplane deps download`", name)
		}
		return "", err
	}
	return path, nil
}

// EnsureSeedSnapshot returns a cached seed snapshot, downloading the seeds
// manifest and snapshot first when needed.
func (m *Manager) EnsureSeedSnapshot(ctx context.Context, name, version string, client *http.Client) (string, error) {
	if path, err := m.CachedSeedSnapshotPath(name, version); err == nil {
		return path, nil
	}
	if client == nil {
		client = http.DefaultClient
	}
	manifest, err := m.fetchSeedsManifest(ctx, client)
	if err != nil {
		return "", fmt.Errorf("failed to load seeds manifest: %w", err)
	}
	if version != "" && manifest.Seeds.Version != version {
		return "", fmt.Errorf("seeds manifest version is %q, want %q", manifest.Seeds.Version, version)
	}
	manifest.ResetCached()
	if err := m.cacheSeedSnapshots(ctx, manifest, "", client, nil); err != nil {
		return "", err
	}
	return m.CachedSeedSnapshotPath(name, version)
}

func (m *Manager) cacheSeedSnapshots(ctx context.Context, manifest *SeedsManifest, manifestPath string, client *http.Client, progress func(DownloadEvent)) error {
	if err := m.populateSeedsCache(ctx, manifest, manifestPath, client, progress); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode cached seeds manifest: %w", err)
	}
	if err := m.WriteCachedSeedsManifest(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (m *Manager) populateSeedsCache(ctx context.Context, manifest *SeedsManifest, manifestPath string, client *http.Client, progress func(DownloadEvent)) error {
	if manifest == nil {
		return fmt.Errorf("seeds manifest is required")
	}
	names := make([]string, 0, len(manifest.Seeds.Snapshots))
	for name := range manifest.Seeds.Snapshots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		snapshot := manifest.Seeds.Snapshots[name]
		if err := validateSeedSnapshot(name, snapshot); err != nil {
			return err
		}
		dest := m.seedSnapshotCachePath(manifest, name, snapshot)
		if snapshot.Path != "" {
			if err := copyLocalSeedSnapshot(dest, snapshot.Path, manifestPath); err != nil {
				return err
			}
			manifest.MarkCached(name)
			if progress != nil {
				progress(DownloadEvent{Type: DownloadEventCached, Name: name, Path: dest})
			}
			continue
		}
		algo, checksum, err := parseSeedSnapshotDigest(name, snapshot)
		if err != nil {
			return err
		}
		cached, err := filecache.Exists(dest, algo, checksum)
		if err != nil {
			return fmt.Errorf("failed to check cache for seed snapshot %s: %w", name, err)
		}
		if cached {
			manifest.MarkCached(name)
			if progress != nil {
				progress(DownloadEvent{Type: DownloadEventCached, Name: name, Path: dest, Current: snapshot.Size, Total: snapshot.Size})
			}
			continue
		}
		if progress != nil {
			progress(DownloadEvent{Type: DownloadEventStarted, Name: name, Path: dest, Total: snapshot.Size})
		}
		_, err = filecache.Download(ctx, snapshot.URL, dest, algo, checksum, filecache.DownloadOptions{
			Client: client,
			Total:  snapshot.Size,
			Progress: func(current, total int64) {
				if progress != nil {
					progress(DownloadEvent{Type: DownloadEventProgress, Name: name, Path: dest, Current: current, Total: total})
				}
			},
		})
		if err != nil {
			if progress != nil {
				progress(DownloadEvent{Type: DownloadEventFailed, Name: name, Path: dest, Err: err})
			}
			return fmt.Errorf("download seed snapshot %s: %w", name, err)
		}
		manifest.MarkCached(name)
		if progress != nil {
			progress(DownloadEvent{Type: DownloadEventDone, Name: name, Path: dest, Current: snapshot.Size, Total: snapshot.Size})
		}
	}
	return nil
}

func (m *Manager) seedSnapshotCachePath(manifest *SeedsManifest, name string, snapshot SeedSnapshot) string {
	filename := name + ".netsy"
	if snapshot.Path != "" {
		filename = filepath.Base(snapshot.Path)
	} else if snapshot.URL != "" {
		if parsed, err := url.Parse(snapshot.URL); err == nil && parsed.Path != "" {
			filename = path.Base(parsed.Path)
		}
	}
	return m.SeedSnapshotCachePath(name, manifest.Seeds.Version, filename)
}

func validateSeedSnapshot(name string, snapshot SeedSnapshot) error {
	if name == "" {
		return fmt.Errorf("seed snapshot name is required")
	}
	if (snapshot.Path == "") == (snapshot.URL == "") {
		return fmt.Errorf("seed snapshot %s must set exactly one of path or url", name)
	}
	if snapshot.URL != "" {
		if _, _, err := parseSeedSnapshotDigest(name, snapshot); err != nil {
			return err
		}
		if snapshot.Size <= 0 {
			return fmt.Errorf("seed snapshot %s is missing size", name)
		}
	}
	return nil
}

func parseSeedSnapshotDigest(name string, snapshot SeedSnapshot) (string, string, error) {
	if snapshot.Digest == "" {
		return "", "", fmt.Errorf("seed snapshot %s is missing digest", name)
	}
	algo, checksum, ok := strings.Cut(snapshot.Digest, ":")
	if !ok {
		return "", "", fmt.Errorf("invalid digest format %q for seed snapshot %s (expected algo:hex)", snapshot.Digest, name)
	}
	if algo != "sha512" {
		return "", "", fmt.Errorf("unsupported digest algorithm %q for seed snapshot %s (supported: sha512)", algo, name)
	}
	if len(checksum) != digestHexLengths[algo] {
		return "", "", fmt.Errorf("invalid sha512 hex length in digest %q for seed snapshot %s", snapshot.Digest, name)
	}
	return algo, checksum, nil
}

func copyLocalSeedSnapshot(dest, source, manifestPath string) error {
	if !filepath.IsAbs(source) && manifestPath != "" {
		source = filepath.Join(filepath.Dir(manifestPath), source)
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open seed snapshot %s: %w", source, err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create seed snapshot cache directory: %w", err)
	}
	tmp := dest + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create seed snapshot cache file %s: %w", tmp, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy seed snapshot %s: %w", source, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close seed snapshot cache file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("move seed snapshot cache file %s to %s: %w", tmp, dest, err)
	}
	return nil
}
