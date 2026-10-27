// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/filecache"
)

// Import verifies and stages a copied bundle before merging its dependencies
// into the cache. It never downloads, executes tools, or changes the source bundle.
func Import(ctx context.Context, cache *deps.Manager, directory, kind, arch string, progress func(deps.DownloadEvent)) error {
	src, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	raw, err := src.ReadFile(manifestFilename)
	if err != nil {
		return fmt.Errorf("read %s (pass the copied podplane-<platform> directory): %w", manifestFilename, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	guestArch, err := platformArch(manifest.Platform)
	if err != nil {
		return err
	}
	if manifest.Format != format || manifest.Kind != kind || manifest.Arch != arch || guestArch != arch {
		return fmt.Errorf("incompatible bundle: format=%d kind=%s arch=%s; need format=%d kind=%s arch=%s", manifest.Format, manifest.Kind, manifest.Arch, format, kind, arch)
	}
	seen := map[string]bool{}
	for _, file := range manifest.Files {
		top := strings.SplitN(file.Path, "/", 2)[0]
		digest, err := hex.DecodeString(file.SHA256)
		if !filecache.ValidPath(file.Path) || seen[file.Path] || file.Size < 0 || err != nil || len(digest) != 32 ||
			(top != "deps" && top != "registry" && top != "bin" && top != "lib" && top != "libexec" && top != "share") {
			return fmt.Errorf("invalid bundle inventory entry %q", file.Path)
		}
		seen[file.Path] = true
	}
	if len(seen) == 0 {
		return fmt.Errorf("bundle inventory is empty")
	}
	// Stage every inventoried file before changing the active cache. This also
	// isolates cache verification from unlisted files and source symlinks.
	stage, err := os.MkdirTemp("", "podplane-import-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	staged, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Close() }()
	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress != nil {
			progress(deps.DownloadEvent{Type: deps.DownloadEventChecking, Name: file.Path, Total: file.Size})
		}
		if err := file.Copy(src, staged, file.Path); err != nil {
			return err
		}
	}
	return cache.Import(ctx, stage, manifest.Files, kind, arch, manifest.Providers, manifest.Addons)
}
