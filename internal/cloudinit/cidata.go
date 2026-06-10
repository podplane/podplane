// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloudinit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

const seedISOSize = 10 * 1024 * 1024

// WriteCloudInitDataISO generates a NoCloud ISO image at outputPath containing
// the provided user-data, meta-data, and network-config strings. The ISO is
// labelled "cidata" so that cloud-init's NoCloud datasource discovers it
// automatically.
func WriteCloudInitDataISO(outputPath, userData, metaData, networkConfig string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create seed ISO directory: %w", err)
	}
	if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to replace seed ISO file: %w", err)
	}

	diskImage, err := diskfs.Create(outputPath, seedISOSize, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("failed to create seed ISO file: %w", err)
	}
	// ISO9660 logical blocks must be 2048 bytes, even when the host disk image
	// helper was created with the default sector size.
	diskImage.LogicalBlocksize = 2048

	fs, err := diskImage.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "cidata",
	})
	if err != nil {
		return fmt.Errorf("failed to create seed ISO filesystem: %w", err)
	}
	defer fs.Close()

	if err := writeSeedFile(fs, "user-data", userData); err != nil {
		return err
	}
	if err := writeSeedFile(fs, "meta-data", metaData); err != nil {
		return err
	}
	if err := writeSeedFile(fs, "network-config", networkConfig); err != nil {
		return err
	}

	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("failed to create seed ISO filesystem: unexpected filesystem type %T", fs)
	}
	if err := iso.Finalize(iso9660.FinalizeOptions{
		RockRidge:        true,
		VolumeIdentifier: "cidata",
	}); err != nil {
		return fmt.Errorf("failed to write seed ISO: %w", err)
	}

	return nil
}

func writeSeedFile(fs filesystem.FileSystem, path, contents string) error {
	f, err := fs.OpenFile(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("failed to add %s to ISO: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, strings.NewReader(contents)); err != nil {
		return fmt.Errorf("failed to write %s to ISO: %w", path, err)
	}

	return nil
}
