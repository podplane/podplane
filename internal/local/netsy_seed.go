// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
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
	valuesContent, err := zotRuntimeValues(zotRegistryEndpoint)
	if err != nil {
		return err
	}
	var data bytes.Buffer
	if err := netsyseed.WriteSnapshot(&data, netsyseed.SnapshotOptions{
		ClusterConfigPath: clusterConfigPath,
		SeedPath:          seedPath,
		ValuesContent:     valuesContent,
	}); err != nil {
		return err
	}
	if err := s3fake.WriteObject(filepath.Join(m.dataDir, "s3"), localNetsyBucketName(m.clusterID), "bootstrap.netsy", data.Bytes()); err != nil {
		return fmt.Errorf("write local Netsy snapshot: %w", err)
	}
	return nil
}

// zotRuntimeValues returns the local Zot endpoint and fake-S3 values overlay.
func zotRuntimeValues(endpoint string) ([]byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid zot registry endpoint %q", endpoint)
	}
	values := map[string]any{"platform": map[string]any{"components": map[string]any{"values": map[string]any{
		"zot-registry": map[string]any{
			"platform": map[string]any{"zotRegistry": map[string]any{"storage": map[string]any{
				"endpoint": endpoint, "secure": true, "skipVerify": true, "forcePathStyle": true,
				"accessKeyID": "test", "secretAccessKey": "test",
			}}},
			"zot": map[string]any{"hostAliases": []map[string]any{{"ip": parsed.Hostname(), "hostnames": []string{"oidc.localhost"}}}},
		},
	}}}}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal local Zot values: %w", err)
	}
	return data, nil
}
