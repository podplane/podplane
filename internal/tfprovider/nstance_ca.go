// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/nstance-dev/nstance/v2/pkg/nonce"
)

type nstanceCAResource struct{}

type nstanceCAModel struct {
	ID             types.String `tfsdk:"id"`
	Provider       types.String `tfsdk:"secrets_provider"`
	SecretPrefix   types.String `tfsdk:"secret_prefix"`
	Region         types.String `tfsdk:"region"`
	Profile        types.String `tfsdk:"profile"`
	Project        types.String `tfsdk:"project"`
	Bucket         types.String `tfsdk:"bucket"`
	ClusterID      types.String `tfsdk:"cluster_id"`
	BackendVersion types.String `tfsdk:"backend_version"`
	Certificate    types.String `tfsdk:"certificate_pem"`
}

// NewNstanceCAResource provisions Nstance's CA before any servers start.
func NewNstanceCAResource() resource.Resource { return &nstanceCAResource{} }

func (*nstanceCAResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nstance_ca"
}

func (*nstanceCAResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Creates or adopts Nstance's Ed25519 CA. The PKCS#8 private key stays in the cloud secret backend; the public certificate is published at cluster/ca.crt. Destroy retains both. Refresh never recreates missing CA material.",
		Attributes: map[string]schema.Attribute{
			"id":               schema.StringAttribute{Computed: true},
			"secrets_provider": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(cloudSecretProviderAWSSSM), PlanModifiers: replace},
			"secret_prefix":    schema.StringAttribute{Required: true, PlanModifiers: replace},
			"region":           schema.StringAttribute{Optional: true, PlanModifiers: replace},
			"profile":          schema.StringAttribute{Optional: true, PlanModifiers: replace},
			"project":          schema.StringAttribute{Optional: true, PlanModifiers: replace},
			"bucket":           schema.StringAttribute{Required: true, Description: "Nstance S3 or GCS bucket containing cluster/ca.crt.", PlanModifiers: replace},
			"cluster_id":       schema.StringAttribute{Required: true, PlanModifiers: replace},
			"backend_version":  schema.StringAttribute{Computed: true},
			"certificate_pem":  schema.StringAttribute{Computed: true, Description: "Public CA certificate only."},
		},
	}
}

func (m nstanceCAModel) keyOptions() (cloudSecretOptions, error) {
	if m.Bucket.ValueString() == "" || m.ClusterID.ValueString() == "" {
		return cloudSecretOptions{}, errors.New("bucket and cluster_id must be non-empty")
	}
	return nstanceSecret(stringOr(m.Provider, cloudSecretProviderAWSSSM), m.SecretPrefix.ValueString(), "ca.key", stringOr(m.Region, ""), stringOr(m.Profile, ""), stringOr(m.Project, ""))
}

func (*nstanceCAResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m nstanceCAModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts, err := m.keyOptions()
	if err != nil {
		resp.Diagnostics.AddError("Invalid Nstance CA configuration", err.Error())
		return
	}
	keyStore, err := newCloudSecretStore(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Configure Nstance CA", "Cannot open secret backend.")
		return
	}
	defer func() { _ = keyStore.Close() }()
	certStore, err := newCACertificateStore(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Configure Nstance CA", "Cannot open certificate bucket.")
		return
	}
	defer func() { _ = certStore.Close() }()
	meta, cert, err := provisionNstanceCA(ctx, keyStore, certStore, m.ClusterID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Provision Nstance CA", err.Error())
		return
	}
	if meta.Identity != opts.Identity() {
		resp.Diagnostics.AddError("Provision Nstance CA", "Unexpected key identity.")
		return
	}
	m.ID = types.StringValue(meta.Identity)
	m.BackendVersion = types.StringValue(meta.Version)
	m.Certificate = types.StringValue(string(cert))
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (*nstanceCAResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m nstanceCAModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	opts, err := m.keyOptions()
	if err != nil {
		resp.Diagnostics.AddError("Invalid Nstance CA configuration", err.Error())
		return
	}
	keyStore, err := newCloudSecretStore(ctx, opts)
	if err != nil {
		resp.Diagnostics.AddError("Read Nstance CA", "Cannot open secret backend.")
		return
	}
	defer func() { _ = keyStore.Close() }()
	meta, err := keyStore.Metadata(ctx)
	if err != nil || meta.Identity != m.ID.ValueString() || meta.Version != m.BackendVersion.ValueString() {
		resp.Diagnostics.AddError("Nstance CA key changed or missing", "Restore the original private key and version; automatic rotation is forbidden.")
		return
	}
	certStore, err := newCACertificateStore(ctx, m)
	if err != nil {
		resp.Diagnostics.AddError("Read Nstance CA", "Cannot open certificate bucket.")
		return
	}
	defer func() { _ = certStore.Close() }()
	cert, err := certStore.Read(ctx)
	if err != nil || string(cert) != m.Certificate.ValueString() {
		resp.Diagnostics.AddError("Nstance CA certificate changed or missing", "Restore the original cluster/ca.crt; automatic replacement is forbidden.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (*nstanceCAResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Nstance CA rotation forbidden", "CA rotation requires an explicit out-of-band operation.")
}

func (*nstanceCAResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}

// provisionNstanceCA publishes only after persisting the key. A retry with just
// the key completes initialization using that same key. An orphan certificate
// must never cause creation of a replacement key. Conditional object writes
// and adopting the winning secret make concurrent creates converge.
func provisionNstanceCA(ctx context.Context, keys cloudSecretStore, certs caCertificateStore, clusterID string) (cloudSecretMetadata, []byte, error) {
	cert, err := certs.Read(ctx)
	if err != nil && !errors.Is(err, errSnapshotAbsent) {
		return cloudSecretMetadata{}, nil, err
	}
	var meta cloudSecretMetadata
	var keyPEM []byte
	if err == nil {
		meta, err = keys.Metadata(ctx)
		if err == nil {
			keyPEM, err = cloudSecretValue(ctx, keys, meta.Version)
		}
	} else {
		var generated []byte
		generated, err = nonce.GenerateSigningKey()
		defer clear(generated)
		if err == nil {
			meta, keyPEM, err = keys.CreateOrAdopt(ctx, generated)
		}
	}
	defer clear(keyPEM)
	if err != nil {
		return meta, nil, errors.New("cannot load or initialize CA key; restore orphaned CA material or resolve backend permissions before retrying")
	}
	key, err := parseNstanceCAKey(keyPEM)
	if err != nil {
		return meta, nil, err
	}
	defer clear(key)
	if cert == nil {
		cert, err = generateNstanceCACertificate(key, clusterID)
		if err != nil {
			return meta, nil, err
		}
		writeErr := certs.Create(ctx, cert)
		// Even a failed response can mean the write committed or another writer
		// won. Always read and validate the stored certificate before succeeding.
		cert, err = certs.Read(ctx)
		if err != nil {
			return meta, nil, fmt.Errorf("CA certificate publication incomplete; retry to finish using the persisted key: %w", errors.Join(writeErr, err))
		}
	}
	if err := validateNstanceCA(cert, key); err != nil {
		return meta, nil, err
	}
	return meta, cert, nil
}

func parseNstanceCAKey(data []byte) (ed25519.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, errors.New("CA key must be a single PKCS#8 PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := value.(ed25519.PrivateKey)
	if err != nil || !ok {
		return nil, errors.New("CA key must be Ed25519; refusing to replace existing material")
	}
	return key, nil
}

// generateNstanceCACertificate matches Nstance v2's CA algorithm and constraints.
func generateNstanceCACertificate(key ed25519.PrivateKey, clusterID string) ([]byte, error) {
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Nstance CA", Organization: []string{"Nstance Cluster " + clusterID}, Country: []string{"US"}},
		NotBefore:    now, NotAfter: now.Add(87600 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true, IsCA: true, MaxPathLen: 1,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, errors.New("cannot generate Nstance CA certificate")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func validateNstanceCA(data []byte, key ed25519.PrivateKey) error {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("invalid Nstance CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return errors.New("invalid Nstance CA certificate")
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if !ok || !bytes.Equal(pub, key.Public().(ed25519.PublicKey)) || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || cert.CheckSignatureFrom(cert) != nil {
		return errors.New("CA certificate does not match the Ed25519 CA key or constraints; refusing to replace existing material")
	}
	if time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
		return errors.New("CA certificate is outside its validity period; automatic renewal is forbidden")
	}
	return nil
}
