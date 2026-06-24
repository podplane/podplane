// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
)

// Delete deletes the local cluster VM and cluster data if it is the last VM
func (m *Local) Delete() error {
	fmt.Println("Deleting local VM disk and runtime files...")
	if err := m.vm.Delete(); err != nil {
		return err
	}
	fmt.Println("Removing local cluster runtime state...")
	if err := removeState(m.runtimeDir, m.clusterID); err != nil {
		return fmt.Errorf("failed to remove state: %w", err)
	}
	fmt.Println("Removing local cluster data...")
	if err := m.deleteClusterData(); err != nil {
		return err
	}
	color.Green("✓ Local cluster deleted successfully")
	return nil
}

// deleteClusterData removes durable local data that belongs only to this
// cluster, while preserving shared local-server keys, TLS assets, and caches.
func (m *Local) deleteClusterData() error {
	if err := os.RemoveAll(ClusterDataDir(m.dataDir, m.clusterID)); err != nil {
		return fmt.Errorf("remove local cluster data: %w", err)
	}
	if err := m.deleteLocalS3ClusterData(); err != nil {
		return err
	}
	if err := m.deleteLocalNstanceClusterData(); err != nil {
		return err
	}
	return nil
}

// deleteLocalS3ClusterData removes fake S3 durable data buckets that are
// derived from the local cluster ID. Cache-backed buckets are intentionally
// preserved.
func (m *Local) deleteLocalS3ClusterData() error {
	for _, bucket := range []string{
		localNetsyBucketName(m.clusterID),
		localTelemetryBucketName(m.clusterID),
	} {
		for _, path := range []string{
			localS3BucketDir(m.dataDir, bucket),
			localS3MetadataDir(m.dataDir, bucket),
		} {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove fake s3 cluster data: %w", err)
			}
		}
	}
	return nil
}

// deleteLocalNstanceClusterData removes fake Nstance tenant, cluster, and
// instance records for this local cluster.
func (m *Local) deleteLocalNstanceClusterData() error {
	root := filepath.Join(m.dataDir, "nstance-fake")
	for _, path := range []string{
		filepath.Join(root, "fakeserver", "tenants", m.clusterID),
		filepath.Join(root, "podplane", "clusters", m.clusterID),
	} {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove fake nstance cluster data: %w", err)
		}
	}

	instancesDir := filepath.Join(root, "fakeserver", "instances")
	entries, err := os.ReadDir(instancesDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read fake nstance instances: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instanceDir := filepath.Join(instancesDir, entry.Name())
		data, err := os.ReadFile(filepath.Join(instanceDir, "instance.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read fake nstance instance metadata: %w", err)
		}
		var instance struct {
			TenantID string `json:"tenant_id"`
		}
		if err := json.Unmarshal(data, &instance); err != nil {
			return fmt.Errorf("decode fake nstance instance metadata: %w", err)
		}
		if instance.TenantID != m.clusterID {
			continue
		}
		if err := os.RemoveAll(instanceDir); err != nil {
			return fmt.Errorf("remove fake nstance instance data: %w", err)
		}
	}
	return nil
}
