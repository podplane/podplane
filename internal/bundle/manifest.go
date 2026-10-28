// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/filecache"
)

const format = 2
const manifestFilename = "podplane-bundle.json"

// Manifest records bundle metadata and a checksum of its contents, without
// replacing the dependency manifests.
type Manifest struct {
	Format          int               `json:"format"`
	CreatedAt       time.Time         `json:"createdAt"`
	Platform        string            `json:"platform"`
	PodplaneVersion string            `json:"podplaneVersion"`
	Kind            string            `json:"kind"`
	Arch            string            `json:"arch"`
	Providers       []string          `json:"providers"`
	Addons          []string          `json:"addons"`
	Tools           map[string]string `json:"tools"`
	ContentsSHA256  string            `json:"contentsSha256"`
}

// inventory reconstructs and verifies the copy entries through a confined root.
func (m Manifest) inventory(root *os.Root) ([]filecache.File, error) {
	if m.Format != format {
		return nil, fmt.Errorf("unsupported bundle format %d", m.Format)
	}
	digest, err := hex.DecodeString(m.ContentsSHA256)
	if err != nil || len(digest) != sha256.Size {
		return nil, fmt.Errorf("invalid bundle contents checksum")
	}
	files, err := contentsInventory(root)
	if err != nil {
		return nil, err
	}
	if contentsChecksum(files) != m.ContentsSHA256 {
		return nil, fmt.Errorf("bundle contents checksum mismatch")
	}
	return files, nil
}

// contentsInventory sorts all regular payload files by slash-separated path,
// excluding bundle metadata, its copy marker and Finder/AppleDouble metadata. Executable
// requirements come from bin/ and libexec/, not filesystem permission bits, so
// moving a bundle through exFAT does not change its checksum or installed modes.
func contentsInventory(root *os.Root) ([]filecache.File, error) {
	files, err := filecache.InventoryFS(root.FS())
	if err != nil {
		return nil, err
	}
	files = slices.DeleteFunc(files, func(file filecache.File) bool {
		name := path.Base(file.Path)
		return file.Path == manifestFilename || file.Path == "."+manifestFilename+".partial" ||
			name == ".DS_Store" || strings.HasPrefix(name, "._")
	})
	if len(files) == 0 {
		return nil, fmt.Errorf("bundle inventory is empty")
	}
	for i := range files {
		top := strings.SplitN(files[i].Path, "/", 2)[0]
		if !filecache.ValidPath(files[i].Path) ||
			(top != "deps" && top != "registry" && top != "bin" && top != "lib" && top != "libexec" && top != "share") {
			return nil, fmt.Errorf("invalid bundle path %q", files[i].Path)
		}
		files[i].Executable = strings.HasPrefix(files[i].Path, "bin/") || strings.HasPrefix(files[i].Path, "libexec/")
	}
	slices.SortFunc(files, func(a, b filecache.File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// contentsChecksum hashes the compact JSON encoding of the sorted inventory.
// Field order and omission of false executable flags are part of format 2.
func contentsChecksum(files []filecache.File) string {
	raw, _ := json.Marshal(files)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
