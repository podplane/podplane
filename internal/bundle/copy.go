// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/filecache"
)

// copyPrepared resumes only copies of this exact inventory. The partial
// inventory identifies our destination; the public inventory is published last.
func copyPrepared(ctx context.Context, source, destination string, progress func(deps.DownloadEvent)) (err error) {
	src, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	raw, err := src.ReadFile(manifestFilename)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Verifying staged podplane-" + manifest.Platform})
	}
	files, err := manifest.inventory(src)
	if err != nil {
		return err
	}
	event := deps.DownloadEvent{Name: "Copy podplane-" + manifest.Platform, Message: "Copying podplane-" + manifest.Platform + "..."}
	for _, file := range files {
		event.Total += file.Size
	}
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Copying verified bundles to " + destination})
		event.Type = deps.DownloadEventStarted
		progress(event)
		defer func() {
			event.Type, event.Err = deps.DownloadEventDone, err
			event.Message = "Copied podplane-" + manifest.Platform + "."
			if err != nil {
				event.Type = deps.DownloadEventFailed
			}
			progress(event)
		}()
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	dst, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	partial := "." + manifestFilename + ".partial"
	owned := false
	for _, name := range []string{manifestFilename, partial} {
		existing, err := dst.ReadFile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, raw) {
			return fmt.Errorf("destination contains a different bundle; choose a new destination")
		}
		owned = true
	}
	if !owned {
		dir, err := dst.Open(".")
		if err != nil {
			return err
		}
		entries, err := dir.ReadDir(1)
		_ = dir.Close()
		if err != nil && err != io.EOF {
			return err
		}
		if len(entries) != 0 {
			return fmt.Errorf("destination is not an empty directory or a resumable Podplane bundle")
		}
	}
	if err := filecache.WriteFile(dst, partial, 0644, func(w io.Writer) error { _, err := w.Write(raw); return err }); err != nil {
		return err
	}
	// A previously complete copy may have been damaged. Do not leave its ready
	// inventory visible while repairing it.
	if err := dst.Remove(manifestFilename); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !file.Matches(dst) {
			if err := file.Copy(src, dst, file.Path); err != nil {
				return err
			}
			if !file.Matches(dst) {
				return fmt.Errorf("destination verification failed: %s", file.Path)
			}
		}
		event.Current += file.Size
		if progress != nil {
			event.Type = deps.DownloadEventProgress
			progress(event)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := manifest.inventory(dst); err != nil {
		return fmt.Errorf("destination verification failed: %w", err)
	}
	return dst.Rename(partial, manifestFilename)
}
