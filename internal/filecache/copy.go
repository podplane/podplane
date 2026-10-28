// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package filecache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// File records a regular file's relative path, digest and executable requirement.
type File struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

// Matches checks a regular file's size, executable requirement and SHA-256 digest.
func (file File) Matches(root *os.Root) bool {
	info, err := root.Lstat(file.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size || (file.Executable && info.Mode()&0111 == 0) {
		return false
	}
	f, err := root.Open(file.Path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return err == nil && hex.EncodeToString(h.Sum(nil)) == file.SHA256
}

// Inventory records paths, sizes, digests and executable bits for a tree,
// rejecting symlinks and other non-regular files.
func Inventory(dir string) ([]File, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return InventoryFS(root.FS())
}

// InventoryFS inventories regular files through the supplied filesystem. Use
// os.Root.FS to confine reads when the tree comes from an untrusted bundle.
func InventoryFS(root fs.FS) ([]File, error) {
	var files []File
	err := fs.WalkDir(root, ".", func(filename string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle contains non-regular file %s", filename)
		}
		f, err := root.Open(filename)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		files = append(files, File{Path: filename, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Executable: info.Mode()&0111 != 0})
		return nil
	})
	return files, err
}

// ValidPath accepts relative slash-separated file paths without traversal,
// backslashes or NUL bytes.
func ValidPath(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\\x00")
}

// WriteFile publishes one file only after the complete write succeeds.
// Root confines both temporary and final paths, including existing symlinks.
func WriteFile(root *os.Root, name string, mode fs.FileMode, write func(io.Writer) error) error {
	if !ValidPath(name) {
		return fmt.Errorf("invalid bundle path %q", name)
	}
	if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
		return err
	}
	tmp := name + "." + rand.Text() + ".tmp"
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	if err := write(out); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

// Copy copies a regular file between confined roots and publishes it only after
// its size and digest match this inventory entry.
func (file File) Copy(src, dst *os.Root, dest string) error {
	info, err := src.Lstat(file.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular bundle file: %s", file.Path)
	}
	in, err := src.Open(file.Path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	mode := fs.FileMode(0644)
	if file.Executable {
		mode = 0755
	}
	return WriteFile(dst, dest, mode, func(w io.Writer) error {
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(w, h), in)
		if err != nil {
			return err
		}
		if n != file.Size || hex.EncodeToString(h.Sum(nil)) != file.SHA256 {
			return fmt.Errorf("bundle checksum mismatch: %s", file.Path)
		}
		return nil
	})
}

// CopyTree inventories and copies a regular-file tree, verifying each copy.
func CopyTree(srcDir, dstDir string) error {
	files, err := Inventory(srcDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	src, err := os.OpenRoot(srcDir)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenRoot(dstDir)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	for _, file := range files {
		if err := file.Copy(src, dst, file.Path); err != nil {
			return err
		}
	}
	return nil
}

// Copy atomically copies a cached file, preserving its permission bits.
func Copy(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	dst, err := os.OpenRoot(filepath.Dir(dest))
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	return WriteFile(dst, filepath.Base(dest), info.Mode().Perm(), func(w io.Writer) error {
		_, err = io.Copy(w, in)
		return err
	})
}

// WriteJSON writes indented JSON with a trailing newline, creating parent directories.
func WriteJSON(filename string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	return os.WriteFile(filename, append(raw, '\n'), 0644)
}
