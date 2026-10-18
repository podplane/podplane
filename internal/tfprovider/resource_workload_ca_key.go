// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*workloadCAKeyResource)(nil)
var _ resource.ResourceWithValidateConfig = (*workloadCAKeyResource)(nil)

// workloadCAProviderValidator restricts workload CA keys to supported secret backends.
type workloadCAProviderValidator struct{}

// Description describes the provider constraint in plain text.
func (workloadCAProviderValidator) Description(context.Context) string {
	return "must be a supported workload CA key provider"
}

// MarkdownDescription describes the provider constraint in Markdown.
func (v workloadCAProviderValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

// ValidateString rejects unsupported secret backends.
func (workloadCAProviderValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	switch req.ConfigValue.ValueString() {
	case cloudSecretProviderAWSSecretsManager, cloudSecretProviderAWSSSM, cloudSecretProviderGCPSecretManager:
	default:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid workload CA key provider", "provider must be one of aws-secrets-manager, aws-parameter-store, or google-secret-manager")
	}
}

// workloadCAKeyResource manages the durable private key for the workload CA.
type workloadCAKeyResource struct{}

// workloadCAKeyModel maps workload CA key arguments and metadata to Terraform state.
type workloadCAKeyModel struct {
	ID                   types.String `tfsdk:"id"`
	Provider             types.String `tfsdk:"secrets_provider"`
	KeyPrefix            types.String `tfsdk:"key_prefix"`
	Region               types.String `tfsdk:"region"`
	Profile              types.String `tfsdk:"profile"`
	Project              types.String `tfsdk:"project"`
	BackendIdentity      types.String `tfsdk:"backend_identity"`
	BackendVersion       types.String `tfsdk:"backend_version"`
	PublicKeyFingerprint types.String `tfsdk:"public_key_fingerprint"`
}

// NewWorkloadCAKeyResource returns the workload CA key resource implementation.
func NewWorkloadCAKeyResource() resource.Resource { return &workloadCAKeyResource{} }

// Metadata returns the resource type name.
func (r *workloadCAKeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workload_ca_key"
}

// Schema returns the workload CA key resource schema.
func (r *workloadCAKeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = resourceschema.Schema{
		Description: "Creates or adopts the dedicated Podplane workload CA key without storing private key bytes in Terraform state. The backend value is retained on destroy.",
		Attributes: map[string]resourceschema.Attribute{
			"id":                     resourceschema.StringAttribute{Computed: true},
			"secrets_provider":       resourceschema.StringAttribute{Required: true, Validators: []validator.String{workloadCAProviderValidator{}}, PlanModifiers: replace},
			"key_prefix":             resourceschema.StringAttribute{Required: true, PlanModifiers: replace},
			"region":                 resourceschema.StringAttribute{Optional: true, PlanModifiers: replace},
			"profile":                resourceschema.StringAttribute{Optional: true, PlanModifiers: replace},
			"project":                resourceschema.StringAttribute{Optional: true, PlanModifiers: replace},
			"backend_identity":       resourceschema.StringAttribute{Computed: true},
			"backend_version":        resourceschema.StringAttribute{Computed: true},
			"public_key_fingerprint": resourceschema.StringAttribute{Computed: true},
		},
	}
}

// ValidateConfig checks provider-specific workload CA key options.
func (r *workloadCAKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config workloadCAKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Provider.IsUnknown() || config.KeyPrefix.IsUnknown() || config.Project.IsUnknown() || config.Region.IsUnknown() || config.Profile.IsUnknown() {
		return
	}
	opts := workloadCAOptions(config)
	if _, err := workloadCAKeySecret(opts); err != nil {
		resp.Diagnostics.AddError("Invalid workload CA key configuration", err.Error())
		return
	}
	if opts.Provider != cloudSecretProviderAWSSecretsManager && opts.Provider != cloudSecretProviderAWSSSM && (opts.Region != "" || opts.Profile != "") {
		resp.Diagnostics.AddError("Invalid workload CA key configuration", "region and profile are only valid for AWS providers")
	}
	if opts.Provider != cloudSecretProviderGCPSecretManager && opts.Project != "" {
		resp.Diagnostics.AddError("Invalid workload CA key configuration", "project is only valid for google-secret-manager")
	}
}

// workloadCAOptions converts Terraform values to workload CA key options.
func workloadCAOptions(model workloadCAKeyModel) workloadCAKeyOptions {
	return workloadCAKeyOptions{
		Provider:  stringOr(model.Provider, ""),
		KeyPrefix: stringOr(model.KeyPrefix, ""),
		Region:    stringOr(model.Region, ""),
		Profile:   stringOr(model.Profile, ""),
		Project:   stringOr(model.Project, ""),
	}
}

// Create generates or adopts a workload CA key and records only its metadata.
func (r *workloadCAKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workloadCAKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := workloadCAOptions(plan)
	secret, err := workloadCAKeySecret(opts)
	if err != nil {
		resp.Diagnostics.AddError("Resolve workload CA key", err.Error())
		return
	}
	store, err := newCloudSecretStore(ctx, secret)
	if err != nil {
		resp.Diagnostics.AddError("Configure workload CA key backend", err.Error())
		return
	}
	defer func() { _ = store.Close() }()
	generated, err := generateWorkloadCAKey()
	if err != nil {
		resp.Diagnostics.AddError("Generate workload CA key", err.Error())
		return
	}
	defer clear(generated)
	metadata, winner, err := store.CreateOrAdopt(ctx, generated)
	if err != nil {
		resp.Diagnostics.AddError("Create or adopt workload CA key", err.Error())
		return
	}
	defer clear(winner)
	fingerprint, err := workloadCAKeyFingerprint(winner)
	if err != nil {
		resp.Diagnostics.AddError("Validate workload CA key", err.Error())
		return
	}
	identity := secret.Identity()
	if metadata.Identity != identity {
		resp.Diagnostics.AddError("Validate workload CA key identity", "backend returned a non-canonical identity")
		return
	}
	plan.ID = types.StringValue(identity)
	plan.BackendIdentity = types.StringValue(identity)
	plan.BackendVersion = types.StringValue(metadata.Version)
	plan.PublicKeyFingerprint = types.StringValue(fingerprint)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read verifies that the workload CA key still has its recorded identity and version.
func (r *workloadCAKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workloadCAKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	secret, err := workloadCAKeySecret(workloadCAOptions(state))
	if err != nil {
		resp.Diagnostics.AddError("Resolve workload CA key", err.Error())
		return
	}
	store, err := newCloudSecretStore(ctx, secret)
	if err != nil {
		resp.Diagnostics.AddError("Configure workload CA key backend", err.Error())
		return
	}
	defer func() { _ = store.Close() }()
	metadata, err := store.Metadata(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Workload CA key loss detected", err.Error())
		return
	}
	if metadata.Identity != state.BackendIdentity.ValueString() || metadata.Version != state.BackendVersion.ValueString() {
		resp.Diagnostics.AddError("Workload CA key loss detected", fmt.Sprintf("backend identity or immutable version changed; restore %s version %s rather than replacing or rotating the key", state.BackendIdentity.ValueString(), state.BackendVersion.ValueString()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update rejects workload CA key rotation through Terraform.
func (r *workloadCAKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Workload CA key rotation is forbidden", "This resource has no update path; all configuration is immutable and key replacement requires an explicit out-of-band rollover.")
}

// Delete forgets the workload CA key while retaining it in the secret backend.
func (r *workloadCAKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
