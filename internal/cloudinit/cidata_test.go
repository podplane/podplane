// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloudinit

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

func TestWriteCloudInitDataISO(t *testing.T) {
	dir := t.TempDir()
	isoPath := filepath.Join(dir, "seed.iso")

	userData := "#!/bin/bash\necho hello"
	metaData := "instance-id: 1\nlocal-hostname: 1\n"

	if err := WriteCloudInitDataISO(isoPath, userData, metaData, ""); err != nil {
		t.Fatalf("WriteCloudInitDataISO failed: %v", err)
	}

	// Verify the ISO was created
	info, err := os.Stat(isoPath)
	if err != nil {
		t.Fatalf("seed ISO not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("seed ISO is empty")
	}

	// Open and verify contents with the ISO reader used by standard disk tooling
	// rather than the writer package itself.
	f, err := os.Open(isoPath)
	if err != nil {
		t.Fatalf("failed to open seed ISO: %v", err)
	}
	defer f.Close()

	img, err := iso9660.Read(file.New(f, true), info.Size(), 0, 2048)
	if err != nil {
		t.Fatalf("failed to open ISO image: %v", err)
	}

	if label := strings.TrimRight(img.Label(), "\x00 "); label != "cidata" {
		t.Fatalf("unexpected ISO label %q", label)
	}

	entries, err := fs.ReadDir(img, ".")
	if err != nil {
		t.Fatalf("failed to read ISO root dir: %v", err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		found[entry.Name()] = true
	}
	for _, name := range []string{"user-data", "meta-data", "network-config"} {
		if !found[name] {
			t.Errorf("%s file not found in ISO", name)
		}
	}

	gotUserData, err := fs.ReadFile(img, "user-data")
	if err != nil {
		t.Fatalf("failed to read user-data from ISO: %v", err)
	}
	if string(gotUserData) != userData {
		t.Fatalf("unexpected user-data content %q", string(gotUserData))
	}
	gotMetaData, err := fs.ReadFile(img, "meta-data")
	if err != nil {
		t.Fatalf("failed to read meta-data from ISO: %v", err)
	}
	if string(gotMetaData) != metaData {
		t.Fatalf("unexpected meta-data content %q", string(gotMetaData))
	}
	gotNetworkConfig, err := fs.ReadFile(img, "network-config")
	if err != nil {
		t.Fatalf("failed to read network-config from ISO: %v", err)
	}
	if string(gotNetworkConfig) != "" {
		t.Fatalf("unexpected network-config content %q", string(gotNetworkConfig))
	}
}

func TestWriteCloudInitDataISO_NetworkConfig(t *testing.T) {
	dir := t.TempDir()
	isoPath := filepath.Join(dir, "seed.iso")

	userData := "#!/bin/bash\necho hello"
	metaData := "instance-id: 1\nlocal-hostname: 1\n"
	networkConfig := "version: 2\nethernets: {}\n"

	if err := WriteCloudInitDataISO(isoPath, userData, metaData, networkConfig); err != nil {
		t.Fatalf("WriteCloudInitDataISO failed: %v", err)
	}

	img := openTestISO(t, isoPath)
	gotNetworkConfig, err := fs.ReadFile(img, "network-config")
	if err != nil {
		t.Fatalf("failed to read network-config from ISO: %v", err)
	}
	if string(gotNetworkConfig) != networkConfig {
		t.Fatalf("unexpected network-config content %q", string(gotNetworkConfig))
	}
}

func TestWriteCloudInitDataISO_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	isoPath := filepath.Join(dir, "nested", "deep", "seed.iso")

	err := WriteCloudInitDataISO(isoPath, "#!/bin/bash", "instance-id: 1\n", "")
	if err != nil {
		t.Fatalf("WriteCloudInitDataISO failed with nested path: %v", err)
	}

	if _, err := os.Stat(isoPath); err != nil {
		t.Fatalf("seed ISO not created at nested path: %v", err)
	}
}

func openTestISO(t *testing.T, isoPath string) *iso9660.FileSystem {
	t.Helper()
	info, err := os.Stat(isoPath)
	if err != nil {
		t.Fatalf("seed ISO not created: %v", err)
	}
	f, err := os.Open(isoPath)
	if err != nil {
		t.Fatalf("failed to open seed ISO: %v", err)
	}
	t.Cleanup(func() {
		_ = f.Close()
	})
	img, err := iso9660.Read(file.New(f, true), info.Size(), 0, 2048)
	if err != nil {
		t.Fatalf("failed to open ISO image: %v", err)
	}
	return img
}
