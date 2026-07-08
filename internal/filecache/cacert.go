// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package filecache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResolveCACert turns a CA Cert spec into an absolute path on disk. The spec
// may be an inline PEM, an http(s) URL, or a filesystem path. Inline PEMs and
// URL responses are cached under cacheDir.
func ResolveCACert(spec, cacheDir string) (string, error) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "":
		return "", nil
	case strings.HasPrefix(spec, "-----BEGIN"):
		return cachePEM(cacheDir, []byte(spec))
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		return cacheURL(cacheDir, spec)
	default:
		abs, err := filepath.Abs(spec)
		if err != nil {
			return "", fmt.Errorf("resolve ca path %s: %w", spec, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("ca cert %s: %w", abs, err)
		}
		return abs, nil
	}
}

func cachePEM(cacheDir string, pem []byte) (string, error) {
	sum := sha256.Sum256(pem)
	path := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".pem")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create ca cache dir %s: %w", cacheDir, err)
	}
	if err := os.WriteFile(path, pem, 0o644); err != nil {
		return "", fmt.Errorf("write ca cache %s: %w", path, err)
	}
	return path, nil
}

func cacheURL(cacheDir, u string) (string, error) {
	sum := sha256.Sum256([]byte(u))
	path := filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".pem")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(u)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: HTTP %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", u, err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create ca cache dir %s: %w", cacheDir, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("write ca cache %s: %w", path, err)
	}
	return path, nil
}
