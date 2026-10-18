// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestSecretProviderConfigValues verifies both key resources accept Nstance names and reject legacy aliases.
func TestSecretProviderConfigValues(t *testing.T) {
	for _, tc := range []struct {
		provider string
		project  string
		valid    bool
	}{
		{"aws-parameter-store", "", true},
		{"aws-secrets-manager", "", true},
		{"google-secret-manager", "test-project", true},
		{"aws_ssm", "", false},
		{"aws_secrets_manager", "", false},
		{"gcp_secret_manager", "test-project", false},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			for _, r := range []resource.ResourceWithValidateConfig{&workloadCAKeyResource{}, &nstanceRegistrationKeyResource{}} {
				var model any = workloadCAKeyModel{Provider: types.StringValue(tc.provider), KeyPrefix: types.StringValue("cluster"), Project: types.StringValue(tc.project)}
				if _, ok := r.(*nstanceRegistrationKeyResource); ok {
					model = nstanceRegistrationKeyModel{Provider: types.StringValue(tc.provider), SecretPrefix: types.StringValue("cluster-"), Project: types.StringValue(tc.project)}
				}
				state := tfsdk.State{Schema: resourceSchema(t, r)}
				if d := state.Set(context.Background(), model); d.HasError() {
					t.Fatal(d)
				}
				var resp resource.ValidateConfigResponse
				r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(state)}, &resp)
				if resp.Diagnostics.HasError() == tc.valid {
					t.Fatalf("%T: valid = %v, diagnostics = %v", r, tc.valid, resp.Diagnostics)
				}
			}
		})
	}
}

// TestWorkloadCAKeySchemaContainsMetadataOnly verifies that private key bytes
// cannot enter configuration or Terraform state.
func TestWorkloadCAKeySchemaContainsMetadataOnly(t *testing.T) {
	schema := resourceSchema(t, &workloadCAKeyResource{})
	for _, name := range []string{"secrets_provider", "key_prefix"} {
		assertStringAttribute(t, schema, name, true, false)
	}
	for _, name := range []string{"region", "profile", "project"} {
		assertStringAttribute(t, schema, name, false, true)
	}
	for _, forbidden := range []string{"value", "private_key", "private_key_pem", "secret", "secret_value", "token", "vault_token", "bao_token"} {
		if _, ok := schema.Attributes[forbidden]; ok {
			t.Fatalf("schema exposes forbidden key attribute %q", forbidden)
		}
	}
}
