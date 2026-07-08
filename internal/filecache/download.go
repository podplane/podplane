// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package filecache

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// DownloadOptions controls how a file download is performed.
type DownloadOptions struct {
	Client   *http.Client
	Progress func(current, total int64)
	Total    int64
}

// Download downloads a file from a url to a path and verifies its checksum
// using the specified algorithm ("sha256" or "sha512"). If checksum is empty,
// the file is written without verification.
func Download(ctx context.Context, url string, path string, algo string, checksum string, opts DownloadOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	dir := filepath.Dir(path)
	filename := filepath.Base(path)
	tmpPath := path + ".tmp"

	// Ensure directory (and all parent directories) of file exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory %s: %w", dir, err)
	}

	_ = os.Remove(tmpPath)

	// Create the file writer
	out, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file %s: %w", tmpPath, err)
	}
	defer func() {
		_ = out.Close()
	}()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	// Get the data
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for %s: %w", url, err)
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download file %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check server response
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("file not found: %s", url)
		}
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	// Create the hash writer
	h, err := newHash(algo)
	if err != nil {
		return "", err
	}

	// Create the progress writer
	total := resp.ContentLength
	if opts.Total > 0 {
		total = opts.Total
	}
	progress := NewProgress(nil, total, 0, func(current, total int64) {
		if opts.Progress != nil {
			opts.Progress(current, total)
			return
		}
		totalMB := float64(total) / 1024 / 1024
		var percentage float64
		if total > 0 {
			percentage = float64(current) / float64(total) * 100
		}
		downloadedMB := float64(current) / 1024 / 1024
		fmt.Printf("\r%.2f%% (%.2f / %.2f MB) %s", percentage, downloadedMB, totalMB, filename)
	})

	// Chain writers
	mw := io.MultiWriter(out, h, progress)

	// Write the body to all writers
	_, err = io.Copy(mw, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write file %s: %w", path, err)
	}
	if opts.Progress != nil {
		opts.Progress(progress.Current, total)
	} else {
		fmt.Printf("\r100%% (%.2f MB) %s\n", float64(total)/1024/1024, filename)
	}

	// Compare checksum
	got := fmt.Sprintf("%x", h.Sum(nil))
	if checksum != "" && got != checksum {
		// Return error
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", path, checksum, got)
	}

	if err := out.Close(); err != nil {
		return "", fmt.Errorf("failed to close file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("failed to move %s to %s: %w", tmpPath, path, err)
	}

	return path, nil
}
