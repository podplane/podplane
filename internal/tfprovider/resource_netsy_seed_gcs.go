// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*netsySeedGCSResource)(nil)

// netsySeedGCSResource creates an initial Netsy snapshot in Google Cloud Storage.
type netsySeedGCSResource struct{}

// netsySeedGCSModel maps Google Cloud Storage seed arguments and results to Terraform state.
type netsySeedGCSModel struct {
	ID                types.String           `tfsdk:"id"`
	ObjectVersion     types.String           `tfsdk:"object_version"`
	NstanceBootstrap  *nstanceBootstrapModel `tfsdk:"nstance_bootstrap"`
	ClusterConfigPath types.String           `tfsdk:"cluster_config_path"`
	SeedPath          types.String           `tfsdk:"seed_path"`
	ValuesFile        types.String           `tfsdk:"values_file"`
	ValuesContent     types.String           `tfsdk:"values_content"`
	Bucket            types.String           `tfsdk:"bucket"`
	Prefix            types.String           `tfsdk:"prefix"`
	Project           types.String           `tfsdk:"project"`
}

// NewNetsySeedGCSResource returns the GCS Netsy seed resource implementation.
func NewNetsySeedGCSResource() resource.Resource {
	return &netsySeedGCSResource{}
}

// Metadata returns the resource type name.
func (r *netsySeedGCSResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_netsy_seed_gcs"
}

// Schema returns the resource schema.
func (r *netsySeedGCSResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceschema.Schema{
		Attributes: map[string]resourceschema.Attribute{
			"id":                resourceschema.StringAttribute{Computed: true},
			"object_version":    resourceschema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"nstance_bootstrap": nstanceBootstrapAttribute(),
			"cluster_config_path": resourceschema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"seed_path": resourceschema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"values_file": resourceschema.StringAttribute{
				Optional:    true,
				Description: "Path to a user-authored YAML or JSON values file, applied last.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"values_content": resourceschema.StringAttribute{
				Optional:    true,
				Description: "Inline YAML or JSON values, applied before values_file.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"bucket": resourceschema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"prefix": resourceschema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"project": resourceschema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

// Create generates the Netsy snapshot, uploads it to GCS, and records state.
func (r *netsySeedGCSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan netsySeedGCSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.ValuesContent.IsUnknown() {
		resp.Diagnostics.AddError("Resolve Netsy seed values", "values_content must be known before creating the Netsy seed")
		return
	}
	if plan.ValuesFile.IsUnknown() {
		resp.Diagnostics.AddError("Resolve Netsy seed values", "values_file must be known before creating the Netsy seed")
		return
	}
	opts := seedOptionsFromGCSModel(plan)
	client, err := newGCSClient(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure Netsy seed storage", err.Error())
		return
	}
	defer func() { _ = client.Close() }()
	meta, err := createSeed(ctx, opts, &gcsSeedStore{client: client, opts: opts}, newCloudSecretStore)
	if err != nil {
		resp.Diagnostics.AddError("Create or adopt Netsy seed", err.Error())
		return
	}
	plan.ObjectVersion = types.StringValue(meta.Version)
	plan.ID = types.StringValue("gs://" + opts.Bucket + "/" + opts.objectKey())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read inspects only object metadata, never the signing key or snapshot payload.
func (r *netsySeedGCSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state netsySeedGCSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := seedOptionsFromGCSModel(state)
	client, err := newGCSClient(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Read Netsy seed metadata", err.Error())
		return
	}
	defer func() { _ = client.Close() }()
	meta, err := (&gcsSeedStore{client: client, opts: opts}).Metadata(ctx)
	if err != nil || meta.Version != state.ObjectVersion.ValueString() || state.NstanceBootstrap != nil && (meta.Completion != seedCompletion(opts) || !meta.Encrypted) {
		resp.Diagnostics.AddError("Netsy seed loss detected", "Cannot verify the original bootstrap object version. Do not re-upload a seed to an initialized cluster.")
		return
	}
	state.ObjectVersion = types.StringValue(meta.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update updates Terraform state; seed-affecting fields require replacement.
func (r *netsySeedGCSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan netsySeedGCSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := seedOptionsFromGCSModel(plan)
	plan.ID = types.StringValue("gs://" + opts.Bucket + "/" + opts.objectKey())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete leaves remote Netsy state intact and removes Terraform state.
func (r *netsySeedGCSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

// seedOptionsFromGCSModel converts Terraform GCS resource state to seed options.
func seedOptionsFromGCSModel(model netsySeedGCSModel) SeedOptions {
	return SeedOptions{
		NstanceBootstrap:  model.NstanceBootstrap,
		ClusterConfigPath: stringOr(model.ClusterConfigPath, ""),
		SeedPath:          stringOr(model.SeedPath, ""),
		ValuesFile:        stringOr(model.ValuesFile, ""),
		ValuesContent:     stringOr(model.ValuesContent, ""),
		Bucket:            stringOr(model.Bucket, ""),
		Prefix:            stringOr(model.Prefix, ""),
		Project:           stringOr(model.Project, ""),
	}
}
