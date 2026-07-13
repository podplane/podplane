// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/s3fake"
	"github.com/podplane/podplane/pkg/netsyseed"
	"github.com/podplane/podplane/pkg/seeds"
)

// ensureInitialNetsySnapshot seeds the local fake S3 bucket for this
// cluster's Netsy datastore with an initial snapshot rendered from the
// cluster config, when seed name is not "none" and the bucket dir is
// empty. Existing local Netsy state is never overwritten.
func (m *Local) ensureInitialNetsySnapshot(clusterConfigPath, depsBaseURL, zotRegistryEndpoint string, seed clusterconfig.Seed) error {
	seedName, err := seeds.ParseName(seed.Name)
	if err != nil {
		return err
	}
	if seedName == seeds.None {
		return nil
	}
	bucketDir := localS3BucketDir(m.dataDir, localNetsyBucketName(m.clusterID))
	entries, err := os.ReadDir(bucketDir)
	if os.IsNotExist(err) {
		entries = nil
	} else if err != nil {
		return fmt.Errorf("check local Netsy bucket %s: %w", bucketDir, err)
	}
	hasData := len(entries) > 0
	if hasData {
		// Preserve any existing local state. Mirrors the Podplane
		// OpenTofu/Terraform provider conditional-put behaviour
		// that ensures we never overwrite a real snapshot.
		return nil
	}
	seedPath, err := seeds.ResolveSeedPath(seeds.ResolveOptions{
		Name:     seedName,
		Version:  seed.Version,
		Digest:   seed.Digest,
		BaseURL:  depsBaseURL,
		CacheDir: m.depsCacheDir,
	})
	if err != nil {
		return err
	}
	if seedPath == "" {
		return nil
	}
	var data bytes.Buffer
	if err := netsyseed.WriteSnapshot(&data, netsyseed.SnapshotOptions{
		ClusterConfigPath:   clusterConfigPath,
		SeedPath:            seedPath,
		ZotRegistryEndpoint: zotRegistryEndpoint,
	}); err != nil {
		return err
	}
	if err := s3fake.WriteObject(filepath.Join(m.dataDir, "s3"), localNetsyBucketName(m.clusterID), "bootstrap.netsy", data.Bytes()); err != nil {
		return fmt.Errorf("write local Netsy snapshot: %w", err)
	}
	return nil
}
