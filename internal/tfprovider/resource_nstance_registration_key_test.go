// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestRegistrationKeyTerraformLifecycle exercises actual framework state and AWS
// SDK requests, including refresh with all plaintext access disabled.
func TestRegistrationKeyTerraformLifecycle(t *testing.T) {
	var mu sync.Mutex
	var value string
	readsForbidden := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		switch req.Header.Get("X-Amz-Target") {
		case "AmazonSSM.PutParameter":
			var in struct {
				Name, Value, Type string
				Overwrite         bool
			}
			if json.NewDecoder(req.Body).Decode(&in) != nil || in.Overwrite || in.Type != "SecureString" || in.Name != "/nstance/test/registration-nonce.key" {
				w.WriteHeader(400)
				return
			}
			if value != "" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"__type":"ParameterAlreadyExists"}`))
				return
			}
			value = in.Value
			_, _ = w.Write([]byte(`{"Version":3}`))
		case "AmazonSSM.GetParameter":
			if readsForbidden {
				w.WriteHeader(403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Parameter": map[string]any{"Name": "/nstance/test/registration-nonce.key", "Type": "SecureString", "Value": value, "Version": 3}})
		case "AmazonSSM.DescribeParameters":
			_, _ = w.Write([]byte(`{"Parameters":[{"Name":"/nstance/test/registration-nonce.key","Type":"SecureString","Version":3}]}`))
		default:
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	t.Setenv("AWS_ENDPOINT_URL_SSM", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "fake-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	r := &nstanceRegistrationKeyResource{}
	schema := resourceSchema(t, r)
	plan := tfsdk.Plan{Schema: schema}
	m := nstanceRegistrationKeyModel{Provider: types.StringValue(cloudSecretProviderAWSSSM), SecretPrefix: types.StringValue("/nstance/test/"), Region: types.StringValue("us-east-1")}
	if d := plan.Set(context.Background(), m); d.HasError() {
		t.Fatal(d)
	}
	create := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	if d := create.State.Get(context.Background(), &m); d.HasError() {
		t.Fatal(d)
	}
	if m.ID.ValueString() != "aws-ssm:/nstance/test/registration-nonce.key" || m.BackendVersion.ValueString() != "3" || len(m.PublicKeyFingerprint.ValueString()) != 64 {
		t.Fatal("incorrect Terraform signer metadata")
	}
	if strings.Contains(create.State.Raw.String(), "PRIVATE KEY") || strings.Contains(create.State.Raw.String(), value) {
		t.Fatal("signing key leaked into Terraform state")
	}
	first := value
	retry := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &retry)
	if retry.Diagnostics.HasError() || !bytes.Equal([]byte(first), []byte(value)) || !retry.State.Raw.Equal(create.State.Raw) {
		t.Fatalf("interrupted apply changed signer state: %v", retry.Diagnostics)
	}
	mu.Lock()
	readsForbidden = true
	mu.Unlock()
	refresh := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(context.Background(), resource.ReadRequest{State: create.State}, &refresh)
	if refresh.Diagnostics.HasError() || !refresh.State.Raw.Equal(create.State.Raw) {
		t.Fatalf("metadata-only refresh failed: %v", refresh.Diagnostics)
	}
	deleted := resource.DeleteResponse{State: create.State}
	r.Delete(context.Background(), resource.DeleteRequest{State: create.State}, &deleted)
	if value != first {
		t.Fatal("destroy deleted or rotated the signer")
	}
}

// TestSeedTerraformMetadataOnlyRecoveryAndRefresh verifies adoption and refresh need no source files or secret reads.
func TestSeedTerraformMetadataOnlyRecoveryAndRefresh(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		t.Run(provider, func(t *testing.T) {
			secretProvider := cloudSecretProviderAWSSSM
			if provider == "gcs" {
				secretProvider = cloudSecretProviderGCPSecretManager
			}
			keyStore, secret := registrationStore(t, secretProvider)
			opts := bootstrapOptions(t, keyStore, secret)
			if err := os.Remove(opts.ClusterConfigPath); err != nil {
				t.Fatal(err)
			}
			// The normal downloaded-seed path would read cluster config unless
			// completed bootstrap adoption happens first.
			opts.SeedPath = ""
			cloud := &snapshotCloud{provider: provider, payload: []byte("payload-must-not-be-read"), completion: seedCompletion(opts)}
			server := httptest.NewServer(cloud)
			defer server.Close()
			t.Setenv("AWS_ENDPOINT_URL_SSM", server.URL)
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", opts.ClusterConfigPath)
			if provider == "s3" {
				t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
				t.Setenv("AWS_ACCESS_KEY_ID", "fake-key")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
				r := &netsySeedS3Resource{}
				state := tfsdk.State{Schema: resourceSchema(t, r)}
				m := netsySeedS3Model{ID: types.StringValue("s3://seed-bucket/netsy/bootstrap.netsy"), Bucket: types.StringValue(opts.Bucket), Prefix: types.StringValue(opts.Prefix), Region: types.StringValue("us-east-1"), ObjectVersion: types.StringValue(`"seed-etag"`), NstanceBootstrap: opts.NstanceBootstrap, ClusterConfigPath: types.StringValue(opts.ClusterConfigPath), ValuesContent: types.StringValue(opts.ValuesContent)}
				if d := state.Set(context.Background(), m); d.HasError() {
					t.Fatal(d)
				}
				create := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
				r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan(state)}, &create)
				if create.Diagnostics.HasError() || !state.Raw.Equal(create.State.Raw) {
					t.Fatalf("completed bootstrap recovery failed: %v", create.Diagnostics)
				}
				resp := resource.ReadResponse{State: tfsdk.State{Schema: state.Schema}}
				r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() || !state.Raw.Equal(resp.State.Raw) {
					t.Fatalf("refresh failed: %v", resp.Diagnostics)
				}
			} else {
				t.Setenv("STORAGE_EMULATOR_HOST", server.URL)
				r := &netsySeedGCSResource{}
				state := tfsdk.State{Schema: resourceSchema(t, r)}
				m := netsySeedGCSModel{ID: types.StringValue("gs://seed-bucket/netsy/bootstrap.netsy"), Bucket: types.StringValue(opts.Bucket), Prefix: types.StringValue(opts.Prefix), Project: types.StringValue(opts.Project), ObjectVersion: types.StringValue("9"), NstanceBootstrap: opts.NstanceBootstrap, ClusterConfigPath: types.StringValue(opts.ClusterConfigPath), ValuesContent: types.StringValue(opts.ValuesContent)}
				if d := state.Set(context.Background(), m); d.HasError() {
					t.Fatal(d)
				}
				create := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
				r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan(state)}, &create)
				if create.Diagnostics.HasError() || !state.Raw.Equal(create.State.Raw) {
					t.Fatalf("completed bootstrap recovery failed: %v", create.Diagnostics)
				}
				resp := resource.ReadResponse{State: tfsdk.State{Schema: state.Schema}}
				r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() || !state.Raw.Equal(resp.State.Raw) {
					t.Fatalf("refresh failed: %v", resp.Diagnostics)
				}
			}
		})
	}
}

// TestSeedRefreshRequiresVersion verifies refresh rejects missing versions and snapshots.
func TestSeedRefreshRequiresVersion(t *testing.T) {
	for _, provider := range []string{"s3", "gcs"} {
		for _, missingSnapshot := range []bool{false, true} {
			version := types.StringNull()
			cloud := &snapshotCloud{provider: provider, payload: []byte("existing snapshot")}
			name := provider + "/missing-version"
			if missingSnapshot {
				version = types.StringValue("recorded-version")
				cloud.payload = nil
				name = provider + "/missing-snapshot"
			}
			t.Run(name, func(t *testing.T) {
				server := httptest.NewServer(cloud)
				defer server.Close()
				t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
				t.Setenv("AWS_ACCESS_KEY_ID", "fake-key")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
				t.Setenv("AWS_SESSION_TOKEN", "")
				t.Setenv("STORAGE_EMULATOR_HOST", server.URL)
				var r resource.Resource = &netsySeedS3Resource{}
				var model any = netsySeedS3Model{ID: types.StringValue("s3://seed-bucket/netsy/bootstrap.netsy"), Bucket: types.StringValue("seed-bucket"), Prefix: types.StringValue("netsy"), Region: types.StringValue("us-east-1"), ObjectVersion: version}
				if provider == "gcs" {
					r = &netsySeedGCSResource{}
					model = netsySeedGCSModel{ID: types.StringValue("gs://seed-bucket/netsy/bootstrap.netsy"), Bucket: types.StringValue("seed-bucket"), Prefix: types.StringValue("netsy"), ObjectVersion: version}
				}
				state := tfsdk.State{Schema: resourceSchema(t, r)}
				if d := state.Set(context.Background(), model); d.HasError() {
					t.Fatal(d)
				}
				resp := resource.ReadResponse{State: state}
				r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
				if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Summary() != "Netsy seed loss detected" || !resp.Diagnostics.HasError() {
					t.Fatalf("missing version or snapshot must fail remote verification: %v", resp.Diagnostics)
				}
			})
		}
	}
}
