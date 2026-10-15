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
	"os"
	"time"
)

var dependencyManifestRetryDelays = []time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
}

// fetchDependencyManifestJSON fetches a dependency manifest URL, retrying
// transient failures, and decodes the response JSON into dst.
func fetchDependencyManifestJSON(ctx context.Context, client *http.Client, url string, dst any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = http.DefaultClient
	}

	var lastErr error
	for attempt := 0; attempt <= len(dependencyManifestRetryDelays); attempt++ {
		if attempt > 0 {
			delay := dependencyManifestRetryDelays[attempt-1]
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("failed to create manifest request for %s: %w", url, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = fmt.Errorf("failed to fetch manifest from %s: %w", url, err)
			continue
		}

		raw, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("failed to read manifest body: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("failed to close manifest body: %w", closeErr)
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("unexpected status %s fetching %s", resp.Status, url)
			if isRetryableManifestStatus(resp.StatusCode) {
				continue
			}
			return lastErr
		}

		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("failed to parse manifest JSON from %s: %w", url, err)
		}
		return nil
	}

	return lastErr
}

// isRetryableManifestStatus reports whether an HTTP status is likely to be a
// transient dependency manifest fetch failure.
func isRetryableManifestStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// fetchManifest downloads and parses the latest vmconfig manifest from
// <baseURL>/manifests/vmconfig_<kind>_<os>_<arch>.json using client.
func (m *Manager) fetchManifest(ctx context.Context, kind, arch string, client *http.Client) (*Manifest, error) {
	url := fmt.Sprintf("%s/manifests/%s", m.baseURL, manifestFilename(kind, arch))
	var manifest Manifest
	if err := fetchDependencyManifestJSON(ctx, client, url, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// readVMConfigManifestFile reads and decodes a local vmconfig JSON manifest.
func readVMConfigManifestFile(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read vmconfig manifest file %s: %w", path, err)
	}

	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse vmconfig manifest JSON from %s: %w", path, err)
	}

	return &manifest, nil
}
