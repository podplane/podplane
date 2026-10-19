// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/go-jose/go-jose/v4"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type serviceAccountKeyResource struct{}

type serviceAccountKeyModel struct {
	ID             types.String `tfsdk:"id"`
	Provider       types.String `tfsdk:"secrets_provider"`
	SecretPrefix   types.String `tfsdk:"secret_prefix"`
	Region         types.String `tfsdk:"region"`
	Profile        types.String `tfsdk:"profile"`
	Project        types.String `tfsdk:"project"`
	BackendVersion types.String `tfsdk:"backend_version"`
	PublicKeyPEM   types.String `tfsdk:"public_key_pem"`
	JWKS           types.String `tfsdk:"jwks_json"`
}

// NewServiceAccountKeyResource returns the retained Kubernetes token signer resource.
func NewServiceAccountKeyResource() resource.Resource { return &serviceAccountKeyResource{} }

// Metadata sets the Terraform resource type name.
func (*serviceAccountKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account_key"
}

// Schema exposes only public keys and backend metadata, never the private key.
func (*serviceAccountKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Creates or adopts the Kubernetes ServiceAccount signer in Nstance's cloud secret backend. Retained on destroy; no automatic rotation. Refresh checks metadata only.",
		Attributes: map[string]schema.Attribute{
			"id":               schema.StringAttribute{Computed: true},
			"secrets_provider": schema.StringAttribute{Required: true, PlanModifiers: replace},
			"secret_prefix":    schema.StringAttribute{Required: true, PlanModifiers: replace},
			"region":           schema.StringAttribute{Optional: true, PlanModifiers: replace},
			"profile":          schema.StringAttribute{Optional: true, PlanModifiers: replace},
			"project":          schema.StringAttribute{Optional: true, PlanModifiers: replace},
			"backend_version":  schema.StringAttribute{Computed: true},
			"public_key_pem":   schema.StringAttribute{Computed: true},
			"jwks_json":        schema.StringAttribute{Computed: true},
		},
	}
}

// options resolves the private key's location in the Nstance secret store.
func (m serviceAccountKeyModel) options() (cloudSecretOptions, error) {
	return nstanceSecret(m.Provider.ValueString(), m.SecretPrefix.ValueString(), "service-accounts.key", m.Region.ValueString(), m.Profile.ValueString(), m.Project.ValueString())
}

// ValidateConfig checks known backend settings before provisioning.
func (*serviceAccountKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m serviceAccountKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Provider.IsUnknown() || m.SecretPrefix.IsUnknown() || m.Region.IsUnknown() || m.Profile.IsUnknown() || m.Project.IsUnknown() {
		return
	}
	if _, err := m.options(); err != nil {
		resp.Diagnostics.AddError("Invalid ServiceAccount signer configuration", err.Error())
	}
}

// Create retains one cluster-wide signer and publishes its verification key.
func (*serviceAccountKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m serviceAccountKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts, err := m.options()
	if err != nil {
		resp.Diagnostics.AddError("Invalid ServiceAccount signer configuration", err.Error())
		return
	}
	store, err := newCloudSecretStore(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure ServiceAccount signer backend", "Cannot configure the cloud secret backend; check credentials and configuration.")
		return
	}
	defer func() { _ = store.Close() }()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		resp.Diagnostics.AddError("Generate ServiceAccount signer", "Cannot generate ECDSA P-256 key.")
		return
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		resp.Diagnostics.AddError("Encode ServiceAccount signer", "Cannot encode ECDSA key.")
		return
	}
	defer clear(der)
	generated := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	defer clear(generated)
	meta, winner, err := store.CreateOrAdopt(ctx, generated)
	defer clear(winner)
	if err != nil {
		resp.Diagnostics.AddError("Create or adopt ServiceAccount signer", "Cannot create or adopt signing key. Check backend permissions and availability; an empty reserved Google secret requires manual recovery, not automatic replacement.")
		return
	}
	public, jwks, err := serviceAccountPublicKey(winner)
	if err != nil {
		resp.Diagnostics.AddError("Validate ServiceAccount signer", err.Error())
		return
	}
	if meta.Identity != opts.Identity() {
		resp.Diagnostics.AddError("Invalid ServiceAccount signer identity", "Backend returned an unexpected identity.")
		return
	}
	m.ID = types.StringValue(meta.Identity)
	m.BackendVersion = types.StringValue(meta.Version)
	m.PublicKeyPEM = types.StringValue(public)
	m.JWKS = types.StringValue(jwks)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// serviceAccountPublicKey derives PEM and JWKS with kube-apiserver's SPKI-hash key ID.
func serviceAccountPublicKey(value []byte) (string, string, error) {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" || strings.TrimSpace(string(rest)) != "" {
		return "", "", fmt.Errorf("existing signer must be a PKCS#8 private key; restore the original key")
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("cannot parse existing signer; restore the original key")
	}
	public := jose.JSONWebKey{Use: "sig"}
	switch key := parsed.(type) {
	case *ecdsa.PrivateKey:
		if key.Curve != elliptic.P256() {
			return "", "", fmt.Errorf("existing ECDSA signer must use P-256 for Google STS compatibility")
		}
		public.Key, public.Algorithm = &key.PublicKey, string(jose.ES256)
	case *rsa.PrivateKey:
		// Retain existing RSA signers rather than rotating deployed clusters.
		if key.N.BitLen() < 2048 || key.Validate() != nil {
			return "", "", fmt.Errorf("existing signer must be a valid RSA key of at least 2048 bits; restore the original key")
		}
		public.Key, public.Algorithm = &key.PublicKey, string(jose.RS256)
	default:
		return "", "", fmt.Errorf("existing signer must be ECDSA P-256 or RSA; restore the original key")
	}
	der, err := x509.MarshalPKIXPublicKey(public.Key)
	if err != nil {
		return "", "", fmt.Errorf("cannot encode verification key")
	}
	hash := sha256.Sum256(der)
	public.KeyID = base64.RawURLEncoding.EncodeToString(hash[:])
	jwks, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{public}})
	if err != nil {
		return "", "", fmt.Errorf("cannot encode JWKS")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), string(jwks), nil
}

// Read detects loss or replacement without reading private key material.
func (*serviceAccountKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m serviceAccountKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts, err := m.options()
	if err != nil {
		resp.Diagnostics.AddError("Invalid ServiceAccount signer configuration", err.Error())
		return
	}
	store, err := newCloudSecretStore(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure ServiceAccount signer backend", "Cannot configure the cloud secret backend.")
		return
	}
	defer func() { _ = store.Close() }()
	meta, err := store.Metadata(ctx)
	if err != nil || meta.Identity != m.ID.ValueString() || meta.Version != m.BackendVersion.ValueString() {
		resp.Diagnostics.AddError("ServiceAccount signer loss detected", "Cannot verify the recorded key identity and immutable version. Restore the original key; no automatic replacement or rotation is performed.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Update rejects implicit signing-key rollover.
func (*serviceAccountKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("ServiceAccount signer rotation forbidden", "All configuration is immutable; rollover requires an explicit out-of-band operation.")
}

// Delete forgets Terraform state without deleting the signing key.
func (*serviceAccountKeyResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
