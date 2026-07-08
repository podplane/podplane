// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package filecache

import (
	"fmt"
	"io"
	"os"
)

// Exists checks if a file already exists at a path and verifies its
// checksum using the specified algorithm ("sha256" or "sha512").
func Exists(path string, algo string, checksum string) (bool, error) {
	if checksum == "" {
		return false, fmt.Errorf("checksum is empty")
	}
	if _, err := os.Stat(path); err == nil {
		f, err := os.Open(path)
		if err == nil {
			defer func() { _ = f.Close() }()
			h, err := newHash(algo)
			if err != nil {
				return false, err
			}
			if _, err := io.Copy(h, f); err == nil {
				got := fmt.Sprintf("%x", h.Sum(nil))
				if got == checksum {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
