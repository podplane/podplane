// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeleteClusterDataRemovesPerClusterData checks cluster cleanup preserves other tenants and buckets.
func TestDeleteClusterDataRemovesPerClusterData(t *testing.T) {
	dataDir := t.TempDir()
	manager := &Local{dataDir: dataDir, clusterID: "cluster-a"}
	paths := []string{
		filepath.Join(ClusterDataDir(dataDir, "cluster-a"), "mutable.env"),
		filepath.Join(ClusterDataDir(dataDir, "cluster-a"), "user-data"),
		ClusterConfigPath(dataDir, "cluster-a"),
		filepath.Join(dataDir, "s3", "buckets", "cluster-a-netsy", "leader", "elector.json"),
		filepath.Join(dataDir, "s3", "metadata", "cluster-a-netsy", "leader", "elector.json"),
		filepath.Join(dataDir, "s3", "buckets", "cluster-a-telemetry", "events.json"),
		filepath.Join(dataDir, "s3", "buckets", "cluster-b-netsy", "leader", "elector.json"),
		filepath.Join(dataDir, "nstance-fake", "fakeserver", "tenants", "cluster-a", "config.json"),
		filepath.Join(dataDir, "nstance-fake", "podplane", "clusters", "cluster-a", "service-accounts.key"),
		filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", "knc-a", "instance.json"),
		filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", "knc-b", "instance.json"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	for _, path := range paths[:9] {
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := os.WriteFile(paths[9], []byte(`{"tenant_id":"cluster-a"}`), 0o600); err != nil {
		t.Fatalf("write %s: %v", paths[9], err)
	}
	if err := os.WriteFile(paths[10], []byte(`{"tenant_id":"cluster-b"}`), 0o600); err != nil {
		t.Fatalf("write %s: %v", paths[10], err)
	}

	if err := manager.deleteClusterData(); err != nil {
		t.Fatalf("deleteClusterData: %v", err)
	}
	removed := []string{
		ClusterDataDir(dataDir, "cluster-a"),
		filepath.Join(dataDir, "s3", "buckets", "cluster-a-netsy"),
		filepath.Join(dataDir, "s3", "metadata", "cluster-a-netsy"),
		filepath.Join(dataDir, "s3", "buckets", "cluster-a-telemetry"),
		filepath.Join(dataDir, "nstance-fake", "fakeserver", "tenants", "cluster-a"),
		filepath.Join(dataDir, "nstance-fake", "podplane", "clusters", "cluster-a"),
		filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", "knc-a"),
	}
	for _, path := range removed {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after cleanup: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", "knc-b")); err != nil {
		t.Fatalf("other tenant instance was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "s3", "buckets", "cluster-b-netsy")); err != nil {
		t.Fatalf("other cluster s3 bucket was removed: %v", err)
	}
}
