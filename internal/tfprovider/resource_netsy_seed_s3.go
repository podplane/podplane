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

var _ resource.Resource = (*netsySeedS3Resource)(nil)

// netsySeedS3Resource creates an initial Netsy snapshot in Amazon S3.
type netsySeedS3Resource struct{}

// netsySeedS3Model maps S3 seed arguments and results to Terraform state.
type netsySeedS3Model struct {
	ID                types.String           `tfsdk:"id"`
	ObjectVersion     types.String           `tfsdk:"object_version"`
	NstanceBootstrap  *nstanceBootstrapModel `tfsdk:"nstance_bootstrap"`
	ClusterConfigPath types.String           `tfsdk:"cluster_config_path"`
	SeedPath          types.String           `tfsdk:"seed_path"`
	ValuesFile        types.String           `tfsdk:"values_file"`
	ValuesContent     types.String           `tfsdk:"values_content"`
	Bucket            types.String           `tfsdk:"bucket"`
	Prefix            types.String           `tfsdk:"prefix"`
	Region            types.String           `tfsdk:"region"`
	Profile           types.String           `tfsdk:"profile"`
}

// NewNetsySeedS3Resource returns the S3 Netsy seed resource implementation.
func NewNetsySeedS3Resource() resource.Resource {
	return &netsySeedS3Resource{}
}

// Metadata returns the resource type name.
func (r *netsySeedS3Resource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_netsy_seed_s3"
}

// Schema returns the resource schema.
func (r *netsySeedS3Resource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
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
			"region": resourceschema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"profile": resourceschema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

// Create generates the Netsy snapshot, uploads it to S3, and records state.
func (r *netsySeedS3Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan netsySeedS3Model
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
	opts := seedOptionsFromS3Model(plan)
	client, err := newS3Client(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure Netsy seed storage", err.Error())
		return
	}
	meta, err := createSeed(ctx, opts, &s3SeedStore{client: client, opts: opts}, newCloudSecretStore)
	if err != nil {
		resp.Diagnostics.AddError("Create or adopt Netsy seed", err.Error())
		return
	}
	plan.ObjectVersion = types.StringValue(meta.Version)
	plan.ID = types.StringValue("s3://" + opts.Bucket + "/" + opts.objectKey())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read inspects only object metadata, never the signing key or snapshot payload.
func (r *netsySeedS3Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state netsySeedS3Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := seedOptionsFromS3Model(state)
	client, err := newS3Client(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Read Netsy seed metadata", err.Error())
		return
	}
	meta, err := (&s3SeedStore{client: client, opts: opts}).Metadata(ctx)
	if err != nil || meta.Version != state.ObjectVersion.ValueString() || state.NstanceBootstrap != nil && (meta.Completion != seedCompletion(opts) || !meta.Encrypted) {
		resp.Diagnostics.AddError("Netsy seed loss detected", "Cannot verify the original bootstrap object version. Do not re-upload a seed to an initialized cluster.")
		return
	}
	state.ObjectVersion = types.StringValue(meta.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update updates Terraform state; seed-affecting fields require replacement.
func (r *netsySeedS3Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan netsySeedS3Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts := seedOptionsFromS3Model(plan)
	plan.ID = types.StringValue("s3://" + opts.Bucket + "/" + opts.objectKey())
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Delete leaves remote Netsy state intact and removes Terraform state.
func (r *netsySeedS3Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

// seedOptionsFromS3Model converts Terraform S3 resource state to seed options.
func seedOptionsFromS3Model(model netsySeedS3Model) SeedOptions {
	return SeedOptions{
		NstanceBootstrap:  model.NstanceBootstrap,
		ClusterConfigPath: stringOr(model.ClusterConfigPath, ""),
		SeedPath:          stringOr(model.SeedPath, ""),
		ValuesFile:        stringOr(model.ValuesFile, ""),
		ValuesContent:     stringOr(model.ValuesContent, ""),
		Bucket:            stringOr(model.Bucket, ""),
		Prefix:            stringOr(model.Prefix, ""),
		Region:            stringOr(model.Region, ""),
		Profile:           stringOr(model.Profile, ""),
	}
}
