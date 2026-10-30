// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Package bundle assembles and imports portable offline Podplane installations.
package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/filecache"
)

// Options selects released host tools and guest dependencies.
type Options struct {
	Platforms []string
	Version   string
	CacheDir  string
	Refresh   bool
	Cleanup   bool
	Client    *http.Client
	Providers []string
	Addons    []string
	Progress  func(deps.DownloadEvent)
}

// Create prepares reusable local staging from the download caches, then writes
// one compressed archive per platform to a destination directory. It returns the
// staging path even when preparation or archiving fails. Cleanup is explicit and
// only happens after every archive succeeds.
func Create(ctx context.Context, cache *deps.Manager, directory, kind string, opts Options) (string, error) {
	if opts.Version != "" && !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`).MatchString(opts.Version) {
		return "", fmt.Errorf("invalid CLI release version %q", opts.Version)
	}
	if len(opts.Platforms) == 0 {
		return "", fmt.Errorf("at least one platform is required")
	}
	// Normalize selection so flag order and destination do not affect reuse.
	opts.Platforms = slices.Clone(opts.Platforms)
	opts.Providers = slices.Clone(opts.Providers)
	opts.Addons = slices.Clone(opts.Addons)
	if len(opts.Providers) == 0 {
		opts.Providers = []string{deps.LocalProviders}
	}
	slices.Sort(opts.Platforms)
	slices.Sort(opts.Providers)
	slices.Sort(opts.Addons)
	var archs []string
	for i, platform := range opts.Platforms {
		arch, err := platformArch(platform)
		if err != nil {
			return "", err
		}
		if i > 0 && platform == opts.Platforms[i-1] {
			return "", fmt.Errorf("duplicate platform %q", platform)
		}
		if !slices.Contains(archs, arch) {
			archs = append(archs, arch)
		}
	}
	// Use the host's normally populated cache before another architecture
	// replaces artifacts that share a version and filename.
	if i := slices.Index(archs, runtime.GOARCH); i > 0 {
		archs[0], archs[i] = archs[i], archs[0]
	}
	// Fail before preparation rather than after minutes of downloads.
	for _, archive := range ArchivePaths(directory, opts.Platforms) {
		if _, err := os.Lstat(archive); err == nil {
			return "", fmt.Errorf("%s already exists; delete it or choose another destination", archive)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if opts.CacheDir == "" {
		opts.CacheDir = filepath.Dir(cache.CacheDir())
	}
	selection, _ := json.Marshal([]any{format, cache.BaseURL(), cache.CacheDir(), kind, opts.Version, opts.Platforms, opts.Providers, opts.Addons})
	stage := filepath.Join(opts.CacheDir, "bundles")
	if err := os.MkdirAll(filepath.Dir(stage), 0755); err != nil {
		return stage, err
	}
	// Serialize builders sharing these caches; an interrupted process releases
	// the lock automatically, unlike an exclusive-create lock file.
	lock, err := os.OpenFile(filepath.Join(opts.CacheDir, "bundles.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return stage, err
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return stage, fmt.Errorf("another bundle command is using %s: %w", opts.CacheDir, err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	if opts.Refresh {
		if err := os.RemoveAll(stage); err != nil {
			return stage, err
		}
	}
	ready := filepath.Join(stage, "ready")
	if previous, err := os.ReadFile(ready); os.IsNotExist(err) {
		if err := prepare(ctx, cache, stage, kind, archs, opts); err != nil {
			return stage, err
		}
		if err := os.WriteFile(ready, selection, 0644); err != nil {
			return stage, err
		}
	} else if err != nil {
		return stage, err
	} else if !bytes.Equal(previous, selection) {
		return stage, fmt.Errorf("staging contains a different bundle selection; rerun with --refresh to replace it")
	}
	for _, platform := range opts.Platforms {
		name := "podplane-" + platform
		if err := archivePrepared(ctx, filepath.Join(stage, name), directory, opts.Progress); err != nil {
			return stage, fmt.Errorf("archive %s (staging retained at %s): %w", name, stage, err)
		}
	}
	if opts.Cleanup {
		return stage, os.RemoveAll(stage)
	}
	return stage, nil
}

// prepare assembles and verifies each architecture before downloading the next,
// because the dependency cache can contain architecture-specific files with the
// same version and basename. Platform inventories are written after verification.
func prepare(ctx context.Context, cache *deps.Manager, stage, kind string, archs []string, opts Options) error {
	downloads := tools{stage: stage, cacheDir: opts.CacheDir, client: opts.Client, progress: opts.Progress}
	versions := map[string]map[string]string{}
	for _, platform := range opts.Platforms {
		directory := filepath.Join(stage, "podplane-"+platform)
		// Only incomplete staging is rebuilt. Persistent downloads and upstream
		// release metadata survive failures, including failures during assembly.
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
		installed, err := downloads.install(ctx, directory, platform, opts.Version)
		if err != nil {
			return err
		}
		versions[platform] = installed
	}
	for i, arch := range archs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := cache.Download(kind, arch, deps.DownloadOptions{
			Archs: archs, Providers: opts.Providers, Addons: opts.Addons,
			SkipCrossArchDependencies: i > 0, Progress: opts.Progress, Client: opts.Client,
		}); err != nil {
			return err
		}
		for _, platform := range opts.Platforms {
			guestArch, _ := platformArch(platform)
			if guestArch != arch {
				continue
			}
			directory := filepath.Join(stage, "podplane-"+platform)
			selected := deps.NewManager("", filepath.Join(directory, "deps"))
			if err := cache.Export(selected, kind, arch, opts.Providers, opts.Addons); err != nil {
				return fmt.Errorf("assemble %s: %w", platform, err)
			}
			if _, err := selected.VerifyOffline(kind, arch, opts.Providers, opts.Addons); err != nil {
				return fmt.Errorf("verify %s: %w", platform, err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				return err
			}
			files, err := contentsInventory(root)
			_ = root.Close()
			if err != nil {
				return err
			}
			manifest := Manifest{
				Format: format, Platform: platform, PodplaneVersion: versions[platform]["podplane"], CreatedAt: time.Now().UTC(),
				Kind: kind, Arch: arch, Providers: opts.Providers, Addons: opts.Addons,
				Tools: versions[platform], ContentsSHA256: contentsChecksum(files),
			}
			if err := filecache.WriteJSON(filepath.Join(directory, manifestFilename), manifest); err != nil {
				return err
			}
		}
	}
	return nil
}

// platformArch validates a supported host platform and returns its guest architecture.
func platformArch(platform string) (string, error) {
	switch platform {
	case "macos-arm64", "linux-arm64":
		return "arm64", nil
	case "macos-amd64", "linux-amd64":
		return "amd64", nil
	default:
		return "", fmt.Errorf("unsupported bundle platform %q (use macos-arm64, macos-amd64, linux-arm64, linux-amd64)", platform)
	}
}
