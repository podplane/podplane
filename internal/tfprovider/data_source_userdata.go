// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/podplane/podplane/pkg/userdata"
)

var _ datasource.DataSource = (*userdataDataSource)(nil)

// userdataDataSource renders Podplane VM userdata.
type userdataDataSource struct{}

// userdataModel maps userdata arguments and results to Terraform state.
type userdataModel struct {
	ManifestJSON               types.String `tfsdk:"manifest_json"`
	DepsMirrorURL              types.String `tfsdk:"deps_mirror_url"`
	ProviderKind               types.String `tfsdk:"provider_kind"`
	AWSAccountID               types.String `tfsdk:"aws_account_id"`
	GoogleProjectID            types.String `tfsdk:"google_project_id"`
	ImmutableSSHAuthorizedKeys types.String `tfsdk:"immutable_ssh_authorized_keys"`
	EnableSSM                  types.Bool   `tfsdk:"enable_ssm"`
	ServerEnv                  types.Map    `tfsdk:"server_env"`
	Content                    types.String `tfsdk:"content"`
}

// NewUserdataDataSource returns the Podplane userdata renderer.
func NewUserdataDataSource() datasource.DataSource {
	return &userdataDataSource{}
}

// Metadata returns the data source type name.
func (d *userdataDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_userdata"
}

// Schema returns the userdata rendering schema.
func (d *userdataDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = datasourceschema.Schema{
		Attributes: map[string]datasourceschema.Attribute{
			"manifest_json": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Pinned vmconfig manifest JSON used to select package versions, URLs, and checksums.",
			},
			"deps_mirror_url": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "Base URL that replaces dependency artifact URLs from the manifest.",
			},
			"provider_kind": datasourceschema.StringAttribute{
				Required:    true,
				Description: "Infrastructure provider kind used to select provider-specific bootstrap steps.",
			},
			"aws_account_id": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "AWS account ID written to immutable user-data environment configuration.",
			},
			"google_project_id": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "Google Cloud project ID written to immutable user-data environment configuration.",
			},
			"immutable_ssh_authorized_keys": datasourceschema.StringAttribute{
				Optional:    true,
				Description: "SSH public keys embedded for early-boot access. Changing this content changes rendered userdata and rotates affected VMs.",
			},
			"enable_ssm": datasourceschema.BoolAttribute{
				Optional:    true,
				Description: "Whether AWS Systems Manager Session Manager bootstrap is included. Defaults to true.",
			},
			"server_env": datasourceschema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Non-secret NST settings provisioned once in mutable.env. Changes require replacing the VM. Only supported for nst manifests.",
			},
			"content": datasourceschema.StringAttribute{
				Computed:    true,
				Description: "Rendered Podplane VM userdata script.",
			},
		},
	}
}

// Read renders userdata deterministically from Terraform configuration.
func (d *userdataDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config userdataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	content, err := renderUserdata(ctx, config)
	if err != nil {
		resp.Diagnostics.AddError("Render Podplane userdata", err.Error())
		return
	}
	config.Content = types.StringValue(content)
	resp.Diagnostics.Append(resp.State.Set(ctx, config)...)
}

// renderUserdata converts Terraform values into canonical renderer inputs.
func renderUserdata(ctx context.Context, config userdataModel) (string, error) {
	if config.ManifestJSON.IsNull() || config.ManifestJSON.IsUnknown() {
		return "", fmt.Errorf("manifest_json must be known")
	}
	if config.ProviderKind.IsNull() || config.ProviderKind.IsUnknown() {
		return "", fmt.Errorf("provider_kind must be known")
	}
	var serverEnv map[string]string
	if !config.ServerEnv.IsNull() {
		if diags := config.ServerEnv.ElementsAs(ctx, &serverEnv, false); diags.HasError() {
			return "", fmt.Errorf("decode server_env: %v", diags)
		}
	}
	return userdata.Render([]byte(config.ManifestJSON.ValueString()), userdata.Options{
		DepsMirrorURL:              stringOr(config.DepsMirrorURL, ""),
		ProviderKind:               config.ProviderKind.ValueString(),
		AWSAccountID:               stringOr(config.AWSAccountID, ""),
		GoogleProjectID:            stringOr(config.GoogleProjectID, ""),
		ImmutableSSHAuthorizedKeys: stringOr(config.ImmutableSSHAuthorizedKeys, ""),
		EnableSSM:                  boolOr(config.EnableSSM, true),
		ServerEnv:                  serverEnv,
	})
}

// boolOr returns a Terraform boolean value or fallback when unset.
func boolOr(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}
