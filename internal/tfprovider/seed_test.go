// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/nstance-dev/nstance/v2/pkg/nonce"
)

// TestSeedOptionsObjectKey verifies bootstrap.netsy is always used under the
// optional normalized prefix.
func TestSeedOptionsObjectKey(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{name: "empty", prefix: "", want: "bootstrap.netsy"},
		{name: "plain", prefix: "netsy", want: "netsy/bootstrap.netsy"},
		{name: "slashes", prefix: "/netsy/", want: "netsy/bootstrap.netsy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SeedOptions{Prefix: tt.prefix}.objectKey()
			if got != tt.want {
				t.Fatalf("objectKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSeedResourceRequiresSnapshot verifies seed resources reject missing seeds and retain real snapshots across retries.
func TestSeedResourceRequiresSnapshot(t *testing.T) {
	var seed bytes.Buffer
	key := "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components"
	if err := datafile.WriteSnapshot(&seed, []*datafile.Record{{Revision: 1, CreateRevision: 1, Version: 1, Created: true, Key: []byte(key), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{}}}`)}}, "generic"); err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(seed.Bytes())
	for _, provider := range []string{"s3", "gcs"} {
		for _, tc := range []struct{ name, config string }{
			{"omitted", ""},
			{"empty", `"seed": {}`},
			{"none", `"seed": {"name":"none"}`},
			{"minimal", fmt.Sprintf(`"seed":{"name":"minimal","version":"v1.0.0","digest":"sha512:%s"}`, hex.EncodeToString(sum[:]))},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				configPath := writeClusterConfig(t, tc.config)
				seedPath := ""
				if tc.name == "minimal" {
					seedPath = filepath.Join(t.TempDir(), "minimal.netsy")
					if err := os.WriteFile(seedPath, seed.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
				}
				cloud := &snapshotCloud{provider: provider}
				server := httptest.NewServer(cloud)
				defer server.Close()
				t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
				t.Setenv("AWS_ACCESS_KEY_ID", "fake-key")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
				t.Setenv("AWS_SESSION_TOKEN", "")
				t.Setenv("STORAGE_EMULATOR_HOST", server.URL)
				var r resource.Resource = &netsySeedS3Resource{}
				var model any = netsySeedS3Model{ClusterConfigPath: types.StringValue(configPath), SeedPath: types.StringValue(seedPath), Bucket: types.StringValue("seed-bucket"), Prefix: types.StringValue("netsy"), Region: types.StringValue("us-east-1")}
				wantVersion := `"seed-etag"`
				if provider == "gcs" {
					r = &netsySeedGCSResource{}
					model = netsySeedGCSModel{ClusterConfigPath: types.StringValue(configPath), SeedPath: types.StringValue(seedPath), Bucket: types.StringValue("seed-bucket"), Prefix: types.StringValue("netsy")}
					wantVersion = "9"
				}
				plan := tfsdk.Plan{Schema: resourceSchema(t, r)}
				if d := plan.Set(context.Background(), model); d.HasError() {
					t.Fatal(d)
				}
				created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &created)
				if tc.name != "minimal" {
					if !created.Diagnostics.HasError() || !strings.Contains(created.Diagnostics[0].Detail(), "seed path is required") || cloud.payload != nil {
						t.Fatalf("missing seed must fail without uploading: %v", created.Diagnostics)
					}
					return
				}
				if created.Diagnostics.HasError() {
					t.Fatal(created.Diagnostics)
				}
				var version string
				if d := created.State.GetAttribute(context.Background(), path.Root("object_version"), &version); d.HasError() || version != wantVersion {
					t.Fatalf("object version = %q, want %q: %v", version, wantVersion, d)
				}
				records, err := datafile.ReadSnapshot(bytes.NewReader(cloud.payload))
				if err != nil || len(records) != 1 || string(records[0].Key) != key {
					t.Fatalf("invalid uploaded snapshot: %v", err)
				}
				for _, file := range []string{configPath, seedPath} {
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				}
				retry := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &retry)
				if retry.Diagnostics.HasError() || !retry.State.Raw.Equal(created.State.Raw) {
					t.Fatalf("snapshot recovery failed: %v", retry.Diagnostics)
				}
			})
		}
	}
}

// TestResolveExplicitSeedPath verifies custom local seeds honor the digest
// pinned in cluster config.
func TestResolveExplicitSeedPath(t *testing.T) {
	contents := []byte("custom seed")
	path := filepath.Join(t.TempDir(), "custom.netsy")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write custom seed: %v", err)
	}
	sum := sha512.Sum512(contents)
	digest := "sha512:" + hex.EncodeToString(sum[:])
	resolved, cleanup, err := resolveExplicitSeedPath(context.Background(), path, digest)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("resolveExplicitSeedPath returned error: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved path = %q, want %q", resolved, path)
	}
	if _, _, err := resolveExplicitSeedPath(context.Background(), path, "sha512:"+strings.Repeat("0", 128)); err == nil {
		t.Fatal("resolveExplicitSeedPath returned nil for mismatched digest")
	}
}

// lockedSecretStore allows concurrent bootstrap tests to share a create-only backend.
type lockedSecretStore struct {
	cloudSecretStore
	mu sync.Mutex
}

// CreateOrAdopt serializes access to the shared test backend.
func (s *lockedSecretStore) CreateOrAdopt(ctx context.Context, value []byte) (cloudSecretMetadata, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cloudSecretStore.CreateOrAdopt(ctx, value)
}

// TestSeedBootstrapCloudCreateAndRecovery checks nonce contents, concurrent creation, and metadata-only recovery.
func TestSeedBootstrapCloudCreateAndRecovery(t *testing.T) {
	for _, provider := range []string{"s3", "gcs", "s3-agentic", "gcs-agentic"} {
		t.Run(provider, func(t *testing.T) {
			agentic := strings.HasSuffix(provider, "-agentic")
			provider = strings.TrimSuffix(provider, "-agentic")
			secretProvider := cloudSecretProviderAWSSSM
			if provider == "gcs" {
				secretProvider = cloudSecretProviderGCPSecretManager
			}
			keyStore, secret := registrationStore(t, secretProvider)
			opts := bootstrapOptions(t, keyStore, secret)
			if agentic {
				config, err := os.ReadFile(opts.ClusterConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(opts.ClusterConfigPath, bytes.ReplaceAll(config, []byte(`"name":"platform"`), []byte(`"name":"agentic"`)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// The pinned seed or explicit values, not its profile name, owns
			// addon membership. This tiny seed uses an explicit values overlay.
			opts.ValuesContent = fmt.Sprintf(`{"platform":{"components":{"apps":{"nstance-operator":{"enabled":true},"sourcegateway":{"enabled":%t}}}}}`, agentic)
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			cloud := &snapshotCloud{provider: provider, ca: bootstrapCA(t), loseResponse: true}
			store := cloudSeedStore(t, provider, opts, cloud)
			gatewaySSM := &registrationSSM{}
			gatewayStore := &lockedSecretStore{cloudSecretStore: &awsSSMStore{client: gatewaySSM, name: "/testcluster/platform-sourcegateway/sourcegateway/encryption-key"}}
			gatewayValue := func() []byte { return gatewaySSM.value }
			if provider == "gcs" {
				backend := &registrationGCP{}
				parent, id := "projects/test-project", "testcluster_platform-sourcegateway_sourcegateway_encryption-key"
				gatewayStore.cloudSecretStore = &gcpSecretStore{client: backend, parent: parent, secretID: id, name: parent + "/secrets/" + id}
				gatewayValue = func() []byte { return backend.value }
			}
			if agentic {
				_, err := createSeed(context.Background(), opts, store, func(_ context.Context, secret cloudSecretOptions) (cloudSecretStore, error) {
					if strings.HasSuffix(secret.Name, "encryption-key") {
						return nil, errors.New("component secret backend unavailable")
					}
					return keyStore, nil
				})
				if err == nil || !strings.Contains(err.Error(), "component secret backend") || cloud.payload != nil {
					t.Fatalf("snapshot must remain unpublished when component provisioning fails: %v", err)
				}
			}
			results := make(chan seedSnapshotMetadata, 2)
			failures := make(chan error, 2)
			for range 2 {
				go func() {
					meta, err := createSeed(context.Background(), opts, store, func(_ context.Context, secret cloudSecretOptions) (cloudSecretStore, error) {
						if strings.HasSuffix(secret.Name, "encryption-key") {
							if secret.Provider != secretProvider {
								return nil, errors.New("wrong Source Gateway secret backend")
							}
							return gatewayStore, nil
						}
						return keyStore, nil
					})
					results <- meta
					failures <- err
				}()
			}
			meta, other := <-results, <-results
			for range 2 {
				if err := <-failures; err != nil {
					t.Fatalf("concurrent create failed: %v", err)
				}
			}
			expectedVersion := `"seed-etag"`
			if provider == "gcs" {
				expectedVersion = "9"
			}
			if meta.Version != expectedVersion || !meta.Encrypted || meta != other {
				t.Fatalf("concurrent creators did not adopt the same encrypted snapshot: %+v / %+v", meta, other)
			}
			if agentic != (len(gatewayValue()) > 0) {
				t.Fatal("Source Gateway key provisioning must follow effective component enablement")
			}
			records, err := datafile.ReadSnapshot(bytes.NewReader(cloud.payload))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 4 {
				t.Fatalf("records = %d, want 4", len(records))
			}
			values := map[string][]byte{}
			for i, record := range records {
				values[string(record.Key)] = record.Value
				if record.Revision != int64(i+1) || record.CreateRevision != record.Revision {
					t.Fatal("non-contiguous bootstrap revisions")
				}
			}
			var configMap struct {
				Data map[string]string `json:"data"`
			}
			if json.Unmarshal(values["/registry/configmaps/platform-nstance/nstance-cluster-ca"], &configMap) != nil || configMap.Data["ca.crt"] != string(cloud.ca) {
				t.Fatal("incorrect public CA ConfigMap")
			}
			var nonceSecret struct {
				Data map[string][]byte `json:"data"`
			}
			if json.Unmarshal(values["/registry/secrets/platform-nstance/nstance-operator-nonce"], &nonceSecret) != nil {
				t.Fatal("invalid bootstrap Secret")
			}
			value, err := cloudSecretValue(context.Background(), keyStore, opts.NstanceBootstrap.SigningKeyVersion.ValueString())
			if err != nil {
				t.Fatal(err)
			}
			defer clear(value)
			block, _ := pem.Decode(value)
			parsed, _ := x509.ParsePKCS8PrivateKey(block.Bytes)
			token := string(nonceSecret.Data["nonce.jwt"])
			claims, err := nonce.Validate(token, parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey), "operator")
			if err != nil || claims.Subject != "testcluster" || claims.ClusterID != "testcluster" || claims.Tenant != "tenant-west" || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 30*time.Minute || claims.ID == "" || claims.Shard != "" || claims.Group != "" {
				t.Fatalf("incorrect nonce claims: %v", err)
			}
			for key, record := range values {
				if len(gatewayValue()) > 0 && bytes.Contains(record, gatewayValue()) {
					t.Fatal("snapshot contains the Source Gateway encryption key")
				}
				if bytes.Contains(record, []byte("PRIVATE KEY")) || bytes.Contains(record, []byte(base64.StdEncoding.EncodeToString(value))) {
					t.Fatal("snapshot contains the private signing key")
				}
				if key != "/registry/secrets/platform-nstance/nstance-operator-nonce" && (bytes.Contains(record, []byte(token)) || bytes.Contains(record, []byte(base64.StdEncoding.EncodeToString([]byte(token))))) {
					t.Fatalf("bootstrap token leaked outside its Secret into %s", key)
				}
			}
			if _, err := nonce.Validate(token, parsed.(ed25519.PrivateKey).Public().(ed25519.PublicKey), "agent"); err == nil {
				t.Fatal("operator nonce accepted as agent credential")
			}
			if _, ok := values["/registry/namespaces/platform-nstance"]; !ok {
				t.Fatal("missing operator namespace")
			}
			// Upload-before-state interruption: remove local source files and make
			// the key backend unavailable. Adoption must still succeed unchanged.
			before := bytes.Clone(cloud.payload)
			if err := os.Remove(opts.SeedPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(opts.ClusterConfigPath); err != nil {
				t.Fatal(err)
			}
			cloud.live = true
			recovered, err := createSeed(context.Background(), opts, store, func(context.Context, cloudSecretOptions) (cloudSecretStore, error) {
				return nil, errors.New("key backend unavailable")
			})
			if err != nil || recovered != meta || !bytes.Equal(before, cloud.payload) {
				t.Fatalf("recovery changed seed: %v", err)
			}
			changed := *opts.NstanceBootstrap
			changed.Tenant = types.StringValue("other-tenant")
			opts.NstanceBootstrap = &changed
			if _, err := createSeed(context.Background(), opts, store, newCloudSecretStore); err == nil {
				t.Fatal("adopted another tenant's bootstrap")
			}
			files, err := os.ReadDir(scratch)
			if err != nil || len(files) != 0 {
				t.Fatal("credential-bearing snapshot generation created temporary files")
			}
		})
	}
}

// TestSeedRejectsLiveStateAndDisabledOverlay checks bootstrap guards and credential-free seeding without bootstrap.
func TestSeedRejectsLiveStateAndDisabledOverlay(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		t.Run(provider, func(t *testing.T) {
			secretProvider := cloudSecretProviderAWSSSM
			if provider == "gcs" {
				secretProvider = cloudSecretProviderGCPSecretManager
			}
			keyStore, secret := registrationStore(t, secretProvider)
			opts := bootstrapOptions(t, keyStore, secret)
			cloud := &snapshotCloud{provider: provider, live: true}
			store := cloudSeedStore(t, provider, opts, cloud)
			if _, err := createSeed(context.Background(), opts, store, newCloudSecretStore); err == nil || cloud.payload != nil {
				t.Fatal("seed overwrote live Netsy state")
			}
			cloud.live = false
			opts.ValuesContent = `{"platform":{"components":{"apps":{"nstance-operator":{"enabled":false}}}}}`
			if _, err := createSeed(context.Background(), opts, store, newCloudSecretStore); err == nil || !strings.Contains(err.Error(), "overlay is disabled") || cloud.payload != nil {
				t.Fatalf("disabled overlay: %v", err)
			}
			// Without bootstrap configuration, even the platform seed is a
			// credential-free snapshot and has no dependency on a key backend.
			opts.NstanceBootstrap = nil
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			if _, err := createSeed(context.Background(), opts, store, func(context.Context, cloudSecretOptions) (cloudSecretStore, error) {
				return nil, errors.New("no signing key configured")
			}); err != nil {
				t.Fatal(err)
			}
			records, err := datafile.ReadSnapshot(bytes.NewReader(cloud.payload))
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || string(records[0].Key) != "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components" {
				t.Fatal("snapshot without bootstrap must contain only the HelmRelease")
			}
			if bytes.Contains(records[0].Value, []byte("PRIVATE KEY")) {
				t.Fatal("generic snapshot contains a private key")
			}
			files, err := os.ReadDir(scratch)
			if err != nil || len(files) != 0 {
				t.Fatal("cluster-specific snapshot generation created temporary files")
			}
		})
	}
}

// writeClusterConfig writes a minimal cluster config with optional seed JSON.
func writeClusterConfig(t *testing.T, seedJSON string) string {
	t.Helper()
	seedBlock := ""
	if seedJSON != "" {
		seedBlock = "," + seedJSON
	}
	path := filepath.Join(t.TempDir(), "podplane.cluster.jsonc")
	contents := `{
  "cluster": {
    "id": "testcluster",
    "oidc": {"issuer_url": "https://oidc.example.com"}` + seedBlock + `
  }
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write cluster config: %v", err)
	}
	return path
}
