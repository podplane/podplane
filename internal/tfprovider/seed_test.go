// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/netsy-dev/netsy/pkg/datafile"
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
