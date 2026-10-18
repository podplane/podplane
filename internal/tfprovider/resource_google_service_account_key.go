// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/secretmanager/v1"
)

type googleServiceAccountKeyResource struct {
	iam     *iam.Service
	secrets *secretmanager.Service
}

type googleServiceAccountKeyModel struct {
	ID             types.String `tfsdk:"id"`
	Project        types.String `tfsdk:"project"`
	ServiceAccount types.String `tfsdk:"service_account"`
	SecretID       types.String `tfsdk:"secret_id"`
	KeyName        types.String `tfsdk:"key_name"`
	SecretVersion  types.String `tfsdk:"secret_version"`
}

// NewGoogleServiceAccountKeyResource returns the retained Google bootstrap key resource.
func NewGoogleServiceAccountKeyResource() resource.Resource {
	return &googleServiceAccountKeyResource{}
}

// Metadata returns the Google bootstrap key resource name.
func (*googleServiceAccountKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_google_service_account_key"
}

// Schema exposes immutable inputs and non-secret metadata only.
func (*googleServiceAccountKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{Description: "Creates or adopts a Google service-account JSON key in a dedicated Secret Manager secret. Only metadata enters state. No rotation; destroy retains both key and secret. Existing empty secrets require manual recovery.", Attributes: map[string]schema.Attribute{
		"project":         schema.StringAttribute{Required: true, PlanModifiers: replace},
		"service_account": schema.StringAttribute{Required: true, PlanModifiers: replace},
		"secret_id":       schema.StringAttribute{Required: true, PlanModifiers: replace},
		"id":              schema.StringAttribute{Computed: true},
		"key_name":        schema.StringAttribute{Computed: true},
		"secret_version":  schema.StringAttribute{Computed: true},
	}}
}

// valid checks the project, secret ID, and service-account email syntax.
func (m googleServiceAccountKeyModel) valid() bool {
	return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]*$`).MatchString(m.Project.ValueString()) &&
		regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`).MatchString(m.SecretID.ValueString()) &&
		regexp.MustCompile(`^[a-zA-Z0-9-]+@[a-zA-Z0-9.-]+\.iam\.gserviceaccount\.com$`).MatchString(m.ServiceAccount.ValueString())
}

// ValidateConfig rejects paths where a simple secret ID or account email is required.
func (*googleServiceAccountKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m googleServiceAccountKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if !resp.Diagnostics.HasError() && !m.Project.IsUnknown() && !m.ServiceAccount.IsUnknown() && !m.SecretID.IsUnknown() && !m.valid() {
		resp.Diagnostics.AddError("Invalid Google key configuration", "Provide a project ID/number, a service-account email, and a simple Secret Manager secret ID.")
	}
}

// clients uses supplied clients or creates clients with application-default credentials.
func (r *googleServiceAccountKeyResource) clients(ctx context.Context) (*iam.Service, *secretmanager.Service, error) {
	i, s := r.iam, r.secrets
	var err error
	if i == nil {
		i, err = iam.NewService(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot configure IAM client")
		}
	}
	if s == nil {
		s, err = secretmanager.NewService(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot configure Secret Manager client")
		}
	}
	return i, s, nil
}

// googleKeyAccount returns the IAM resource path for the configured service account.
func googleKeyAccount(m googleServiceAccountKeyModel) string {
	return "projects/-/serviceAccounts/" + m.ServiceAccount.ValueString()
}

// googleKeyValid checks that the account's key is enabled, user-managed, and within its validity period.
func googleKeyValid(ctx context.Context, i *iam.Service, m googleServiceAccountKeyModel, name string) bool {
	prefix := googleKeyAccount(m) + "/keys/"
	if !strings.HasPrefix(name, prefix) || !regexp.MustCompile(`^[A-Za-z0-9]+$`).MatchString(strings.TrimPrefix(name, prefix)) {
		return false
	}
	k, err := i.Projects.ServiceAccounts.Keys.Get(name).Context(ctx).Do()
	if err != nil || k.Disabled || k.KeyType != "USER_MANAGED" {
		return false
	}
	now := time.Now()
	for _, bound := range []struct {
		value string
		start bool
	}{{k.ValidAfterTime, true}, {k.ValidBeforeTime, false}} {
		if bound.value == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, bound.value)
		if err != nil || (bound.start && now.Before(t)) || (!bound.start && !now.Before(t)) {
			return false
		}
	}
	return true
}

// googleKeyPayload validates the credential JSON and returns its verified IAM key name.
func googleKeyPayload(ctx context.Context, i *iam.Service, m googleServiceAccountKeyModel, data []byte) (string, bool) {
	var key struct {
		Type    string `json:"type"`
		Email   string `json:"client_email"`
		ID      string `json:"private_key_id"`
		Private string `json:"private_key"`
	}
	if json.Unmarshal(data, &key) != nil || key.Type != "service_account" || key.Email != m.ServiceAccount.ValueString() || !strings.HasPrefix(key.Private, "-----BEGIN PRIVATE KEY-----") {
		return "", false
	}
	block, rest := pem.Decode([]byte(key.Private))
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return "", false
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	private, ok := parsed.(*rsa.PrivateKey)
	if err != nil || !ok || private.N.BitLen() < 2048 || private.Validate() != nil {
		return "", false
	}
	name := googleKeyAccount(m) + "/keys/" + key.ID
	return name, googleKeyValid(ctx, i, m, name)
}

// Update forbids automatic rollover.
func (*googleServiceAccountKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Google key rotation forbidden", "All inputs are immutable; rollover requires an explicit out-of-band operation.")
}

// Delete forgets state without revoking credentials or removing the secret.
func (*googleServiceAccountKeyResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

// Create reserves the secret before issuing a key, or adopts its existing payload.
func (r *googleServiceAccountKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m googleServiceAccountKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fail := func(detail string) { resp.Diagnostics.AddError("Cannot create or adopt Google key", detail) }
	if !m.valid() {
		fail("Invalid project, service-account email, or simple secret ID.")
		return
	}
	i, s, err := r.clients(ctx)
	if err != nil {
		fail(err.Error())
		return
	}
	parent := "projects/" + m.Project.ValueString()
	requested := parent + "/secrets/" + m.SecretID.ValueString()
	secret, err := s.Projects.Secrets.Create(parent, &secretmanager.Secret{Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}}}).SecretId(m.SecretID.ValueString()).Context(ctx).Do()
	created := err == nil
	if err != nil {
		apiErr, ok := err.(*googleapi.Error)
		if !ok || apiErr.Code != 409 {
			fail("Secret reservation failed or is indeterminate; inspect the dedicated secret before retrying. No key was requested.")
			return
		}
		secret, err = s.Projects.Secrets.Get(requested).Context(ctx).Do()
	}
	if err != nil || secret == nil || secret.Name == "" {
		fail("Cannot resolve reserved secret metadata; no key was requested.")
		return
	}
	m.ID = types.StringValue(secret.Name)
	var data []byte
	var version string
	if !created {
		out, accessErr := s.Projects.Secrets.Versions.Access(secret.Name + "/versions/latest").Context(ctx).Do()
		if accessErr != nil || out.Payload == nil {
			fail("Existing secret has no accessible enabled version. Do not populate it automatically: check for an active provisioner, restore the original version, or manually remove a confirmed empty reservation and any orphan IAM key.")
			return
		}
		data, err = base64.StdEncoding.DecodeString(out.Payload.Data)
		version = out.Name
	} else {
		key, issueErr := i.Projects.ServiceAccounts.Keys.Create(googleKeyAccount(m), &iam.CreateServiceAccountKeyRequest{PrivateKeyType: "TYPE_GOOGLE_CREDENTIALS_FILE", KeyAlgorithm: "KEY_ALG_RSA_2048"}).Context(ctx).Do()
		if issueErr != nil {
			fail("IAM key issuance failed or is indeterminate. Reservation retained: inspect account keys for an orphan before manually removing the empty secret and retrying.")
			return
		}
		data, err = base64.StdEncoding.DecodeString(key.PrivateKeyData)
		key.PrivateKeyData = ""
	}
	defer clear(data)
	if err != nil {
		fail("Invalid key encoding; reservation and any IAM key retained for manual recovery.")
		return
	}
	name, valid := googleKeyPayload(ctx, i, m, data)
	if !valid {
		fail("Payload does not identify an enabled, valid IAM key for the expected account. Restore the original key; nothing was deleted.")
		return
	}
	m.KeyName = types.StringValue(name)
	if created {
		out, publishErr := s.Projects.Secrets.AddVersion(secret.Name, &secretmanager.AddSecretVersionRequest{Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(data)}}).Context(ctx).Do()
		if publishErr != nil {
			// A timeout can follow a successful write. Never revoke a potentially published key.
			fail("Publication failed or is indeterminate. IAM key and secret retained. Retry to adopt a published version; if empty, inspect IAM keys and manually recover the reservation. No automatic reissuance or deletion is performed.")
			return
		}
		version = out.Name
	}
	if !googleKeyVersion(secret.Name, version) {
		fail("Missing immutable version metadata; key and secret retained. Retry adoption after inspecting the secret.")
		return
	}
	m.SecretVersion = types.StringValue(version)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// googleKeyVersion checks that version names a numbered version of the given secret.
func googleKeyVersion(secret, version string) bool {
	return strings.HasPrefix(version, secret+"/versions/") && regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(strings.TrimPrefix(version, secret+"/versions/"))
}

// Read verifies the recorded key and version using metadata-only API calls.
func (r *googleServiceAccountKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m googleServiceAccountKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	i, s, err := r.clients(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot verify Google key", err.Error())
		return
	}
	secret, secretErr := s.Projects.Secrets.Get("projects/" + m.Project.ValueString() + "/secrets/" + m.SecretID.ValueString()).Context(ctx).Do()
	if !m.valid() || secretErr != nil || secret.Name != m.ID.ValueString() || !googleKeyVersion(m.ID.ValueString(), m.SecretVersion.ValueString()) || !googleKeyValid(ctx, i, m, m.KeyName.ValueString()) {
		resp.Diagnostics.AddError("Google key loss detected", "Recorded IAM key is missing, disabled, expired, or invalid. Restore it explicitly; no automatic rotation is performed.")
		return
	}
	v, err := s.Projects.Secrets.Versions.Get(m.SecretVersion.ValueString()).Context(ctx).Do()
	if err != nil || v.Name != m.SecretVersion.ValueString() || v.State != "ENABLED" {
		resp.Diagnostics.AddError("Google key secret loss detected", "Cannot verify the recorded enabled secret version. Restore it explicitly; no secret payload was read.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
