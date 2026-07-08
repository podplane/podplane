// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package osboot

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend"
	"github.com/lima-vm/go-qcow2reader"
	qcow2image "github.com/lima-vm/go-qcow2reader/image"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/filecache"
	"github.com/podplane/podplane/internal/vm"
)

// Options configures direct boot artifact preparation for one cached OS image.
type Options struct {
	ImagePath string
	CacheDir  string
	Boot      deps.BootInfo
}

// imageFile is one manifest-selected file to extract from a partition in the
// cached OS image and write to a verified host-side cache path.
type imageFile struct {
	Partition   int
	Path        string
	Algo        string
	Checksum    string
	Destination string
}

// Prepare uses complete manifest os.boot metadata to ensure the named kernel
// and initrd are extracted from the cached OS image and available in CacheDir.
func Prepare(opts Options) (*vm.DirectBootOptions, error) {
	// Cache extracted artifacts by their source basenames under the caller's
	// image-specific boot cache directory.
	kernelPath := filepath.Join(opts.CacheDir, path.Base(opts.Boot.Kernel.Path))
	initrdPath := filepath.Join(opts.CacheDir, path.Base(opts.Boot.Initrd.Path))

	// Convert manifest boot artifact entries into extraction requests with
	// parsed digest fields.
	kernelAlgo, kernelChecksum, err := (deps.Dependency{Digest: opts.Boot.Kernel.Digest}).ParseDigest()
	if err != nil {
		return nil, fmt.Errorf("invalid kernel boot digest: %w", err)
	}
	initrdAlgo, initrdChecksum, err := (deps.Dependency{Digest: opts.Boot.Initrd.Digest}).ParseDigest()
	if err != nil {
		return nil, fmt.Errorf("invalid initrd boot digest: %w", err)
	}
	files := []imageFile{
		{
			Partition:   opts.Boot.Kernel.Partition,
			Path:        opts.Boot.Kernel.Path,
			Algo:        kernelAlgo,
			Checksum:    kernelChecksum,
			Destination: kernelPath,
		},
		{
			Partition:   opts.Boot.Initrd.Partition,
			Path:        opts.Boot.Initrd.Path,
			Algo:        initrdAlgo,
			Checksum:    initrdChecksum,
			Destination: initrdPath,
		},
	}

	// Reuse already-extracted artifacts when their contents still match the
	// manifest digests; only extract missing or corrupt files.
	var missing []imageFile
	for _, file := range files {
		exists, err := filecache.Exists(file.Destination, file.Algo, file.Checksum)
		if err != nil {
			return nil, fmt.Errorf("check cached boot artifact %s: %w", file.Destination, err)
		}
		if !exists {
			missing = append(missing, file)
		}
	}

	// Extract any missing artifacts from the cached base qcow2.
	if len(missing) > 0 {
		if err := extractFiles(opts.ImagePath, missing); err != nil {
			return nil, err
		}
	}

	return &vm.DirectBootOptions{
		KernelPath: kernelPath,
		InitrdPath: initrdPath,
		Cmdline:    opts.Boot.Cmdline,
	}, nil
}

func extractFiles(imagePath string, files []imageFile) error {
	if len(files) == 0 {
		return nil
	}
	raw, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("open qcow2 image %s: %w", imagePath, err)
	}
	defer func() { _ = raw.Close() }()

	img, err := qcow2reader.Open(raw)
	if err != nil {
		return fmt.Errorf("open qcow2 reader for %s: %w", imagePath, err)
	}
	storage := &qcow2Storage{img: img, name: filepath.Base(imagePath), size: img.Size()}
	disk, err := diskfs.OpenBackend(storage, diskfs.WithSectorSize(diskfs.SectorSize512))
	if err != nil {
		_ = storage.Close()
		return fmt.Errorf("open disk image %s: %w", imagePath, err)
	}
	defer func() { _ = disk.Close() }()

	filesystems := map[int]fs.ReadFileFS{}
	for _, file := range files {
		if file.Partition <= 0 {
			return fmt.Errorf("invalid partition %d for %s", file.Partition, file.Path)
		}
		if file.Path == "" || file.Destination == "" {
			return fmt.Errorf("invalid boot extraction path for partition %d", file.Partition)
		}
		filesystem, ok := filesystems[file.Partition]
		if !ok {
			opened, err := disk.GetFilesystem(file.Partition)
			if err != nil {
				return fmt.Errorf("open partition %d filesystem: %w", file.Partition, err)
			}
			filesystem = opened
			filesystems[file.Partition] = opened
		}
		if err := extractFile(filesystem, file); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(filesystem fs.ReadFileFS, file imageFile) error {
	algo, checksum, err := normalizeDigest(file.Algo, file.Checksum)
	if err != nil {
		return fmt.Errorf("invalid digest for %s: %w", file.Path, err)
	}
	fsPaths := filesystemPaths(file.Path)
	if len(fsPaths) == 0 {
		return fmt.Errorf("invalid source path %q", file.Path)
	}
	var data []byte
	var readErr error
	for _, fsPath := range fsPaths {
		data, readErr = filesystem.ReadFile(fsPath)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		return fmt.Errorf("read %s from partition %d: %w", file.Path, file.Partition, readErr)
	}
	h, err := newHash(algo)
	if err != nil {
		return err
	}
	if _, err := h.Write(data); err != nil {
		return err
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != checksum {
		return fmt.Errorf("digest mismatch for %s: expected %s:%s, got %s:%s", file.Path, algo, checksum, algo, got)
	}

	if err := os.MkdirAll(filepath.Dir(file.Destination), 0755); err != nil {
		return fmt.Errorf("create boot cache directory: %w", err)
	}
	tmpPath := file.Destination + ".tmp"
	defer func() { _ = os.Remove(tmpPath) }()
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write boot artifact %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, file.Destination); err != nil {
		return fmt.Errorf("cache boot artifact %s: %w", file.Destination, err)
	}
	return nil
}

func filesystemPaths(manifestPath string) []string {
	fsPath := strings.TrimPrefix(path.Clean(manifestPath), "/")
	if fsPath == "." || fsPath == "" {
		return nil
	}
	paths := []string{fsPath}
	// Debian cloud images commonly store kernels on a dedicated /boot
	// partition. The manifest path specifies the guest-visible path, but once that
	// partition is opened as a filesystem the leading boot/ mountpoint is no
	// longer present.
	if stripped := strings.TrimPrefix(fsPath, "boot/"); stripped != fsPath && stripped != "" {
		paths = append(paths, stripped)
	}
	return paths
}

func normalizeDigest(algo, checksum string) (string, string, error) {
	if algo != "" && checksum != "" {
		return algo, checksum, nil
	}
	return "", "", fmt.Errorf("missing digest")
}

func newHash(algo string) (hash.Hash, error) {
	switch algo {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported digest algorithm %q", algo)
	}
}

type qcow2Storage struct {
	img    qcow2image.Image
	name   string
	size   int64
	offset int64
}

var _ backend.Storage = (*qcow2Storage)(nil)

func (s *qcow2Storage) Stat() (fs.FileInfo, error) {
	return qcow2FileInfo{name: s.name, size: s.size}, nil
}

func (s *qcow2Storage) Read(p []byte) (int, error) {
	n, err := s.ReadAt(p, s.offset)
	s.offset += int64(n)
	return n, err
}

func (s *qcow2Storage) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	want := len(p)
	if int64(len(p)) > s.size-off {
		p = p[:int(s.size-off)]
	}
	n, err := s.img.ReadAt(p, off)
	if err == nil && n < want {
		err = io.EOF
	}
	return n, err
}

func (s *qcow2Storage) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = s.offset + offset
	case io.SeekEnd:
		next = s.size + offset
	default:
		return 0, fmt.Errorf("invalid seek whence %d", whence)
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek offset %d", next)
	}
	s.offset = next
	return s.offset, nil
}

func (s *qcow2Storage) Close() error {
	return s.img.Close()
}

func (s *qcow2Storage) Sys() (*os.File, error) {
	return nil, backend.ErrNotSuitable
}

func (s *qcow2Storage) Writable() (backend.WritableFile, error) {
	return nil, backend.ErrIncorrectOpenMode
}

func (s *qcow2Storage) Path() string {
	return ""
}

type qcow2FileInfo struct {
	name string
	size int64
}

func (i qcow2FileInfo) Name() string       { return i.name }
func (i qcow2FileInfo) Size() int64        { return i.size }
func (i qcow2FileInfo) Mode() fs.FileMode  { return 0644 }
func (i qcow2FileInfo) ModTime() time.Time { return time.Time{} }
func (i qcow2FileInfo) IsDir() bool        { return false }
func (i qcow2FileInfo) Sys() interface{}   { return nil }
