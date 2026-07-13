// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"crypto/sha512"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/vm/qemu"
	"github.com/podplane/podplane/pkg/seeds"
)

const componentsHelmReleaseKey = "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components"

const testSeedVersion = "v1.2.3-1"

// TestLocalComponentsSourceUsesBranchForDevManifest verifies development
// components manifests track the main branch instead of a nonexistent vdev tag.
func TestLocalComponentsSourceUsesBranchForDevManifest(t *testing.T) {
	manager := deps.NewManager("https://example.invalid", t.TempDir())
	if err := manager.WriteCachedComponentsManifest([]byte(`{"components":{"version":"dev"}}`)); err != nil {
		t.Fatalf("WriteCachedComponentsManifest: %v", err)
	}

	source, err := localComponentsSource(manager, clusterconfig.Seed{Name: seeds.Recommended, Version: testSeedVersion}, "10.0.2.15")
	if err != nil {
		t.Fatalf("localComponentsSource: %v", err)
	}
	if source == nil {
		t.Fatalf("source is nil")
	}
	if got, want := source.Ref.Branch, "main"; got != want {
		t.Fatalf("source.Ref.Branch = %q, want %q", got, want)
	}
	if source.Ref.Tag != "" {
		t.Fatalf("source.Ref.Tag = %q, want empty", source.Ref.Tag)
	}
}

// TestLocalComponentsSourceUsesSemverForReleasedManifest verifies released
// components manifests keep using matching semver selectors for reproducibility.
func TestLocalComponentsSourceUsesSemverForReleasedManifest(t *testing.T) {
	manager := deps.NewManager("https://example.invalid", t.TempDir())
	if err := manager.WriteCachedComponentsManifest([]byte(`{"components":{"version":"1.2.1","source":{"url":"https://github.com/podplane/components.git","ref":{"tag":"v1.2.1"}}}}`)); err != nil {
		t.Fatalf("WriteCachedComponentsManifest: %v", err)
	}

	source, err := localComponentsSource(manager, clusterconfig.Seed{Name: seeds.Recommended, Version: testSeedVersion}, "10.0.2.15")
	if err != nil {
		t.Fatalf("localComponentsSource: %v", err)
	}
	if source == nil {
		t.Fatalf("source is nil")
	}
	if got, want := source.Ref.Semver, "^1.2.1"; got != want {
		t.Fatalf("source.Ref.Semver = %q, want %q", got, want)
	}
	if source.Ref.Branch != "" {
		t.Fatalf("source.Ref.Branch = %q, want empty", source.Ref.Branch)
	}
}

// TestEnsureInitialNetsySnapshotNoneSkips verifies that seed name "none" leaves
// the local Netsy bucket uncreated.
func TestEnsureInitialNetsySnapshotNoneSkips(t *testing.T) {
	dir := t.TempDir()
	m := &Local{dataDir: dir, clusterID: "dev"}
	cfgPath := writeMinimalLocalClusterConfig(t, dir, "dev")
	if err := m.ensureInitialNetsySnapshot(cfgPath, "", "", clusterconfig.Seed{Name: seeds.None}); err != nil {
		t.Fatalf("ensureInitialNetsySnapshot: %v", err)
	}
	bucket := localS3BucketDir(dir, localNetsyBucketName("dev"))
	if _, err := os.Stat(bucket); !os.IsNotExist(err) {
		t.Fatalf("expected bucket dir to not exist, got err=%v", err)
	}
}

// TestEnsureInitialNetsySnapshotSkipsNonEmptyBucket verifies existing Netsy
// state is preserved instead of overwritten.
func TestEnsureInitialNetsySnapshotSkipsNonEmptyBucket(t *testing.T) {
	dir := t.TempDir()
	m := &Local{dataDir: dir, clusterID: "dev"}
	bucket := localS3BucketDir(dir, localNetsyBucketName("dev"))
	existing := filepath.Join(bucket, "snapshots", "0000000000000000001.netsy")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(existing, []byte("prior"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	cfgPath := writeMinimalLocalClusterConfig(t, dir, "dev")

	if err := m.ensureInitialNetsySnapshot(cfgPath, "http://127.0.0.1:0", "https://10.0.2.15:19443/s3/cache", clusterconfig.Seed{Name: seeds.Recommended, Version: testSeedVersion}); err != nil {
		t.Fatalf("ensureInitialNetsySnapshot: %v", err)
	}
	// Existing file must be untouched (a real seed would have hit a server
	// at port 0 and errored if it tried to reach the network).
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing: %v", err)
	}
	if string(got) != "prior" {
		t.Fatalf("existing file was modified: %q", got)
	}
}

// TestEnsureInitialNetsySnapshotWritesSnapshot verifies the rendered Netsy
// snapshot file is written to the local fake-S3 bucket.
func TestEnsureInitialNetsySnapshotWritesSnapshot(t *testing.T) {
	dir := t.TempDir()
	m := &Local{dataDir: dir, clusterID: "dev", depsCacheDir: filepath.Join(dir, "deps")}
	cfgPath := writeMinimalLocalClusterConfig(t, dir, "dev")

	seedBytes := buildSeedSnapshot(t, "dev", 7)
	digest := sha512.Sum512(seedBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifests/seeds.json":
			_, _ = fmt.Fprintf(w, `{"seeds":{"version":%q,"snapshots":{"recommended":{"url":"http://%s/netsy/recommended.netsy","digest":"sha512:%x","size":%d}}}}`, testSeedVersion, r.Host, digest, len(seedBytes))
		case "/netsy/recommended.netsy":
			_, _ = w.Write(seedBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	if err := m.ensureInitialNetsySnapshot(cfgPath, server.URL, "https://10.0.2.15:19443/s3/cache", clusterconfig.Seed{Name: seeds.Recommended, Version: testSeedVersion, Digest: fmt.Sprintf("sha512:%x", digest)}); err != nil {
		t.Fatalf("ensureInitialNetsySnapshot: %v", err)
	}
	want := filepath.Join(localS3BucketDir(dir, localNetsyBucketName("dev")), "bootstrap.netsy")
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("expected snapshot at %s: %v", want, err)
	}
	if info.Size() == 0 {
		t.Fatalf("snapshot is empty")
	}
	metadataDir := localS3MetadataDir(dir, localNetsyBucketName("dev"))
	metadataEntries, err := os.ReadDir(metadataDir)
	if err != nil {
		t.Fatalf("read snapshot metadata dir: %v", err)
	}
	foundMetadata := false
	for _, entry := range metadataEntries {
		if strings.HasPrefix(entry.Name(), "bootstrap.netsy-") {
			foundMetadata = true
			break
		}
	}
	if !foundMetadata {
		t.Fatalf("expected fake S3 metadata for bootstrap.netsy in %s", metadataDir)
	}
}

// TestGetSeedConfigReadsSavedValue verifies restarts keep the cluster.seed
// value saved in cluster.jsonc.
func TestGetSeedConfigReadsSavedValue(t *testing.T) {
	dir := t.TempDir()
	m := newTestLocalManager(dir, "dev")
	if _, err := m.WriteLocalClusterConfig("dev", "https://oidc.localhost:1/oidc", "/tmp/ca.pem", LocalKubernetesAPIHostname("dev"), 4433, clusterconfig.Seed{Name: seeds.None}, nil); err != nil {
		t.Fatalf("WriteLocalClusterConfig: %v", err)
	}
	raw, err := os.ReadFile(ClusterConfigPath(dir, "dev"))
	if err != nil {
		t.Fatalf("read cluster config: %v", err)
	}
	if !strings.Contains(string(raw), `"seed": {}`) {
		t.Fatalf("cluster config should render empty seed object when seed is none:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"hostname": "dev-registry.local"`) {
		t.Fatalf("cluster config should include local registry mirror hostname:\n%s", raw)
	}
	wantVaultAddress := fmt.Sprintf(`"address": "https://%s:19443/vault/dev"`, m.vm.NodeIP())
	if !strings.Contains(string(raw), wantVaultAddress) {
		t.Fatalf("cluster config should use stable local Vault forward:\n%s", raw)
	}
	if strings.Contains(string(raw), `10.0.2.2`) {
		t.Fatalf("cluster config should not persist host-side local server addresses:\n%s", raw)
	}
	m.clusterID = "dev"
	seed, err := m.SeedConfig()
	if err != nil {
		t.Fatalf("SeedConfig: %v", err)
	}
	if seed.Name != seeds.None {
		t.Fatalf("SeedConfig name = %q, want %q", seed.Name, seeds.None)
	}
}

func TestSeedConfigRejectsMissingConfig(t *testing.T) {
	m := &Local{dataDir: t.TempDir(), clusterID: "dev"}
	if _, err := m.SeedConfig(); err == nil {
		t.Fatalf("expected missing config error")
	}
}

// writeMinimalLocalClusterConfig writes the smallest local cluster config used
// by Netsy seed tests and returns its path.
func writeMinimalLocalClusterConfig(t *testing.T, dataDir, clusterID string) string {
	t.Helper()
	manager := newTestLocalManager(dataDir, clusterID)
	path, err := manager.WriteLocalClusterConfig(clusterID, "https://oidc.localhost:1/oidc", "/tmp/ca.pem", LocalKubernetesAPIHostname(clusterID), 4433, clusterconfig.Seed{Name: seeds.Recommended, Version: testSeedVersion, Digest: "sha512:" + strings.Repeat("0", 128)}, nil)
	if err != nil {
		t.Fatalf("WriteLocalClusterConfig: %v", err)
	}
	return path
}

func newTestLocalManager(dataDir, clusterID string) *Local {
	return &Local{
		dataDir:   dataDir,
		clusterID: clusterID,
		vm: qemu.NewQemu(qemu.Options{
			ClusterID:  clusterID,
			DataDir:    dataDir,
			RuntimeDir: dataDir,
		}),
	}
}

// buildSeedSnapshot returns a tiny Podplane seed file containing the
// platform-components HelmRelease record.
func buildSeedSnapshot(t *testing.T, clusterID string, revision int64) []byte {
	t.Helper()
	records := []*datafile.Record{
		{Revision: revision, Key: []byte(componentsHelmReleaseKey), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{}}}`)},
	}
	var buf bytes.Buffer
	if err := datafile.WriteSnapshot(&buf, records, clusterID); err != nil {
		t.Fatalf("write seed snapshot: %v", err)
	}
	return buf.Bytes()
}
