// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeleteRemovesStoppedVMArtifacts checks deletion removes disks, the serial socket, and PID file.
func TestDeleteRemovesStoppedVMArtifacts(t *testing.T) {
	dataDir := t.TempDir()
	runtimeDir := t.TempDir()
	m := &Qemu{
		arch:       "arm64",
		clusterID:  "cluster-a",
		dataDir:    dataDir,
		runtimeDir: runtimeDir,
		VMName:     "podplane-local-cluster-a",
	}

	for _, path := range []string{m.VMImagePath(), m.CloudInitDataDiskPath(), m.SerialConsolePath()} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	pidFile, err := m.VMPIDFile()
	if err != nil {
		t.Fatalf("VMPIDFile: %v", err)
	}
	if err := pidFile.Write(); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	if err := m.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, path := range []string{m.VMImagePath(), m.CloudInitDataDiskPath(), m.SerialConsolePath(), pidFile.FilePath()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after delete: %v", path, err)
		}
	}
}
