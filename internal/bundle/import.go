// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/deps"
)

// Import verifies and stages a copied bundle before merging its dependencies
// into the cache. It never downloads, executes tools, or changes the source bundle.
func Import(ctx context.Context, cache *deps.Manager, directory, kind, arch string, progress func(deps.DownloadEvent)) (err error) {
	src, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	raw, err := src.ReadFile(manifestFilename)
	if err != nil {
		return fmt.Errorf("read %s (pass the extracted podplane-<platform> directory): %w", manifestFilename, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	guestArch, err := platformArch(manifest.Platform)
	if err != nil {
		return err
	}
	if manifest.Kind != kind || manifest.Arch != arch || guestArch != arch {
		return fmt.Errorf("incompatible bundle: kind=%s arch=%s; need kind=%s arch=%s", manifest.Kind, manifest.Arch, kind, arch)
	}
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Verifying bundle contents"})
	}
	files, err := manifest.inventory(src)
	if err != nil {
		return err
	}
	event := deps.DownloadEvent{Name: "Import podplane-" + manifest.Platform, Message: "Importing podplane-" + manifest.Platform + "..."}
	for _, file := range files {
		event.Total += file.Size
	}
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Verifying and staging bundle contents"})
		event.Type = deps.DownloadEventStarted
		progress(event)
		defer func() {
			event.Type, event.Err = deps.DownloadEventDone, err
			event.Message = "Imported podplane-" + manifest.Platform + "."
			if err != nil {
				event.Type = deps.DownloadEventFailed
			}
			progress(event)
		}()
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
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := file.Copy(src, staged, file.Path); err != nil {
			return err
		}
		event.Current += file.Size
		if progress != nil {
			event.Type = deps.DownloadEventProgress
			progress(event)
		}
	}
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Verifying dependencies and merging into the local cache"})
	}
	return cache.Import(ctx, stage, files, kind, arch, manifest.Providers, manifest.Addons)
}
