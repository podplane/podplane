// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/internal/deps"
)

// ArchivePaths returns the archive each platform writes in directory.
func ArchivePaths(directory string, platforms []string) []string {
	var paths []string
	for _, platform := range platforms {
		paths = append(paths, filepath.Join(directory, "podplane-"+platform+".tar.gz"))
	}
	return paths
}

// archivePrepared streams a verified staged bundle directly into a new
// <directory>/podplane-<platform>.tar.gz, writing the destination only once.
// Entries live under one podplane-<platform>/ directory, starting with the
// bundle manifest, so system tar extracts the same tree that deps import
// accepts. An existing archive is never replaced, and a failed write removes
// the incomplete file it created.
func archivePrepared(ctx context.Context, source, directory string, progress func(deps.DownloadEvent)) (err error) {
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
	prefix := "podplane-" + manifest.Platform
	name := prefix + ".tar.gz"
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Verifying staged " + prefix})
	}
	files, err := manifest.inventory(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	dst, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	out, err := dst.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if os.IsExist(err) {
		return fmt.Errorf("%s already exists; delete it or choose another destination", filepath.Join(directory, name))
	}
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = dst.Remove(name)
		}
	}()
	event := deps.DownloadEvent{Name: "Archive " + prefix, Message: "Compressing " + name + "..."}
	for _, file := range files {
		event.Total += file.Size
	}
	if progress != nil {
		progress(deps.DownloadEvent{Type: deps.DownloadEventStatus, Message: "Writing compressed bundles to " + directory})
		event.Type = deps.DownloadEventStarted
		progress(event)
		defer func() {
			event.Type, event.Err = deps.DownloadEventDone, err
			event.Message = "Wrote " + name + "."
			if err != nil {
				event.Type = deps.DownloadEventFailed
			}
			progress(event)
		}()
	}
	// Favor the smallest transfer; only the online builder pays the time.
	gz, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	// Fixed metadata keeps archives of the same staging reproducible.
	header := func(path string, mode, size int64) *tar.Header {
		return &tar.Header{Name: prefix + "/" + path, Mode: mode, Size: size, ModTime: manifest.CreatedAt, Format: tar.FormatPAX}
	}
	if err := tw.WriteHeader(header(manifestFilename, 0644, int64(len(raw)))); err != nil {
		return err
	}
	if _, err := tw.Write(raw); err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode := int64(0644)
		if file.Executable {
			mode = 0755
		}
		if err := tw.WriteHeader(header(file.Path, mode, file.Size)); err != nil {
			return err
		}
		in, err := src.Open(file.Path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(tw, h), in)
		_ = in.Close()
		if err != nil {
			return fmt.Errorf("archive %s: %w", file.Path, err)
		}
		if hex.EncodeToString(h.Sum(nil)) != file.SHA256 {
			return fmt.Errorf("bundle checksum mismatch: %s", file.Path)
		}
		event.Current += file.Size
		if progress != nil {
			event.Type = deps.DownloadEventProgress
			progress(event)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
