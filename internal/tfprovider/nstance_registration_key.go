// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nstance-dev/nstance/v2/pkg/nonce"
)

// nstanceRegistrationKeyResource retains Nstance's long-lived nonce signer.
type nstanceRegistrationKeyResource struct{}

// nstanceRegistrationKeyModel maps signer configuration and public metadata to Terraform state.
type nstanceRegistrationKeyModel struct {
	ID                   types.String `tfsdk:"id"`
	Provider             types.String `tfsdk:"secrets_provider"`
	SecretPrefix         types.String `tfsdk:"secret_prefix"`
	Region               types.String `tfsdk:"region"`
	Profile              types.String `tfsdk:"profile"`
	Project              types.String `tfsdk:"project"`
	BackendVersion       types.String `tfsdk:"backend_version"`
	PublicKeyFingerprint types.String `tfsdk:"public_key_fingerprint"`
}

// NewNstanceRegistrationKeyResource returns the Nstance registration signing-key resource.
func NewNstanceRegistrationKeyResource() resource.Resource { return &nstanceRegistrationKeyResource{} }

// Metadata sets the Terraform resource type name.
func (*nstanceRegistrationKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nstance_registration_key"
}

// Schema defines signer configuration and metadata without exposing private key bytes.
func (*nstanceRegistrationKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = resourceschema.Schema{
		Description: "Creates or adopts Nstance's PKCS#8 Ed25519 registration signer in an encrypted cloud secret backend. Never rotates or deletes the key; refresh reads metadata only.",
		Attributes: map[string]resourceschema.Attribute{
			"id":                     resourceschema.StringAttribute{Computed: true},
			"secrets_provider":       resourceschema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(cloudSecretProviderAWSSSM), PlanModifiers: replace},
			"secret_prefix":          resourceschema.StringAttribute{Required: true, Description: "Exact Nstance cluster.secrets.prefix, including its trailing separator. No separator is added.", PlanModifiers: replace},
			"region":                 resourceschema.StringAttribute{Optional: true, PlanModifiers: replace},
			"profile":                resourceschema.StringAttribute{Optional: true, PlanModifiers: replace},
			"project":                resourceschema.StringAttribute{Optional: true, PlanModifiers: replace},
			"backend_version":        resourceschema.StringAttribute{Computed: true},
			"public_key_fingerprint": resourceschema.StringAttribute{Computed: true},
		},
	}
}

// nstanceSigningSecret follows Nstance's backend naming, not workload CA naming.
func nstanceSigningSecret(provider, prefix, region, profile, project string) (cloudSecretOptions, error) {
	if prefix == "" {
		return cloudSecretOptions{}, fmt.Errorf("secret_prefix must be non-empty")
	}
	opts := cloudSecretOptions{Provider: provider, Region: region, Profile: profile, Project: project}
	switch provider {
	case cloudSecretProviderAWSSSM, cloudSecretProviderAWSSecretsManager:
		if project != "" {
			return opts, fmt.Errorf("project is only valid for google-secret-manager")
		}
		opts.Name = prefix + "registration-nonce.key"
	case cloudSecretProviderGCPSecretManager:
		if project == "" || strings.Contains(project, "/") || region != "" || profile != "" {
			return opts, fmt.Errorf("google-secret-manager requires project and does not accept region or profile")
		}
		for _, c := range prefix {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return opts, fmt.Errorf("secret_prefix for Google must contain only letters, digits, hyphens, and underscores")
			}
		}
		opts.Name = "projects/" + project + "/secrets/" + prefix + "registration-nonce-key"
	default:
		return opts, fmt.Errorf("provider must be aws-parameter-store, aws-secrets-manager, or google-secret-manager")
	}
	return opts, nil
}

// nstanceKeyOptions resolves Terraform settings into a signing-key backend location.
func nstanceKeyOptions(m nstanceRegistrationKeyModel) (cloudSecretOptions, error) {
	return nstanceSigningSecret(stringOr(m.Provider, cloudSecretProviderAWSSSM), m.SecretPrefix.ValueString(), stringOr(m.Region, ""), stringOr(m.Profile, ""), stringOr(m.Project, ""))
}

// ValidateConfig checks known signing-key settings against backend naming rules.
func (*nstanceRegistrationKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m nstanceRegistrationKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Provider.IsUnknown() || m.SecretPrefix.IsUnknown() || m.Region.IsUnknown() || m.Profile.IsUnknown() || m.Project.IsUnknown() {
		return
	}
	if _, err := nstanceKeyOptions(m); err != nil {
		resp.Diagnostics.AddError("Invalid Nstance signer configuration", err.Error())
	}
}

// Create creates or adopts the signing key and records only its public metadata.
func (*nstanceRegistrationKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m nstanceRegistrationKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts, err := nstanceKeyOptions(m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Nstance signer configuration", err.Error())
		return
	}
	store, err := newCloudSecretStore(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure Nstance signer backend", "Cannot configure the cloud secret backend; check credentials and backend configuration.")
		return
	}
	defer func() { _ = store.Close() }()
	meta, fingerprint, err := createNstanceSigningKey(ctx, store)
	if err != nil {
		resp.Diagnostics.AddError("Create or adopt Nstance signer", err.Error())
		return
	}
	if meta.Identity != opts.Identity() {
		resp.Diagnostics.AddError("Invalid Nstance signer identity", "The backend returned an unexpected identity.")
		return
	}
	m.ID = types.StringValue(meta.Identity)
	m.BackendVersion = types.StringValue(meta.Version)
	m.PublicKeyFingerprint = types.StringValue(fingerprint)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// createNstanceSigningKey creates or adopts a PKCS#8 Ed25519 key and fingerprints its public key.
func createNstanceSigningKey(ctx context.Context, store cloudSecretStore) (cloudSecretMetadata, string, error) {
	generated, err := nonce.GenerateSigningKey()
	if err != nil {
		return cloudSecretMetadata{}, "", fmt.Errorf("cannot generate Ed25519 signing key")
	}
	defer clear(generated)
	meta, winner, err := store.CreateOrAdopt(ctx, generated)
	defer clear(winner)
	if err != nil {
		return cloudSecretMetadata{}, "", fmt.Errorf("cannot create or adopt signing key; check backend permissions and availability; an existing empty Google secret must be resolved manually, never populated or rotated automatically")
	}
	fingerprint, err := nonce.FingerprintSigningKey(winner)
	if err != nil {
		return cloudSecretMetadata{}, "", fmt.Errorf("existing signing key is not a PKCS#8 Ed25519 private key; restore the original key instead of replacing it")
	}
	return meta, fingerprint, nil
}

// Read verifies the recorded signing-key identity and version without reading its value.
func (*nstanceRegistrationKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m nstanceRegistrationKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts, err := nstanceKeyOptions(m)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Nstance signer configuration", err.Error())
		return
	}
	store, err := newCloudSecretStore(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure Nstance signer backend", "Cannot configure the cloud secret backend.")
		return
	}
	defer func() { _ = store.Close() }()
	meta, err := store.Metadata(ctx)
	if err != nil || meta.Identity != m.ID.ValueString() || meta.Version != m.BackendVersion.ValueString() {
		resp.Diagnostics.AddError("Nstance signer loss detected", "Cannot verify the recorded key identity and immutable version. Restore the original key; Terraform must not replace or rotate it.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Update rejects signing-key rotation through Terraform.
func (*nstanceRegistrationKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Nstance signer rotation forbidden", "The signing key has no update path. Rollover requires an explicit out-of-band operation.")
}

// Delete removes Terraform state while retaining the signing key in its backend.
func (*nstanceRegistrationKeyResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
