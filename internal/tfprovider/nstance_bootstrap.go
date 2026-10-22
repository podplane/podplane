// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/tailscale/hujson"

	"github.com/podplane/podplane/pkg/seeds"
)

const (
	operatorNonceTTL       = 30 * time.Minute
	publicCAWait           = 5 * time.Minute
	seedCompletionMetadata = "podplane-seed"
)

// nstanceBootstrapModel contains references and scope only, never credentials.
type nstanceBootstrapModel struct {
	Provider              types.String `tfsdk:"provider"`
	SecretPrefix          types.String `tfsdk:"secret_prefix"`
	SigningKeyID          types.String `tfsdk:"signing_key_id"`
	SigningKeyVersion     types.String `tfsdk:"signing_key_version"`
	SigningKeyFingerprint types.String `tfsdk:"signing_key_fingerprint"`
	ClusterID             types.String `tfsdk:"cluster_id"`
	Tenant                types.String `tfsdk:"tenant"`
	Namespace             types.String `tfsdk:"namespace"`
	CABucket              types.String `tfsdk:"ca_bucket"`
	CAPrefix              types.String `tfsdk:"ca_prefix"`
	CAConfigMap           types.String `tfsdk:"ca_config_map"`
	NonceSecret           types.String `tfsdk:"nonce_secret"`
}

// nstanceBootstrapAttribute defines the optional seed bootstrap references and defaults.
func nstanceBootstrapAttribute() resourceschema.SingleNestedAttribute {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	required := resourceschema.StringAttribute{Required: true, PlanModifiers: replace}
	return resourceschema.SingleNestedAttribute{
		Optional:      true,
		PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		Description:   "Non-secret Nstance v2 operator bootstrap references. Omit when the AWS/Google platform or agentic provider overlay is disabled. Credentials are generated only during Create and inserted directly into the snapshot, never Helm values.",
		Attributes: map[string]resourceschema.Attribute{
			"provider":                resourceschema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(cloudSecretProviderAWSSSM), PlanModifiers: replace},
			"secret_prefix":           required,
			"signing_key_id":          required,
			"signing_key_version":     required,
			"signing_key_fingerprint": required,
			"cluster_id":              required,
			"tenant":                  required,
			"namespace":               required,
			"ca_bucket":               required,
			"ca_prefix":               resourceschema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("cluster"), PlanModifiers: replace},
			"ca_config_map":           resourceschema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("nstance-cluster-ca"), PlanModifiers: replace},
			"nonce_secret":            resourceschema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("nstance-operator-nonce"), PlanModifiers: replace},
		},
	}
}

// secret resolves the signing-key backend and validates its recorded metadata references.
func (b *nstanceBootstrapModel) secret(opts SeedOptions) (cloudSecretOptions, error) {
	secret, err := nstanceSecret(b.Provider.ValueString(), b.SecretPrefix.ValueString(), "registration-nonce.key", opts.Region, opts.Profile, opts.Project)
	if err != nil {
		return secret, err
	}
	if secret.Identity() != b.SigningKeyID.ValueString() || b.SigningKeyVersion.ValueString() == "" || len(b.SigningKeyFingerprint.ValueString()) != 64 {
		return secret, errors.New("bootstrap must reference the signing-key resource's identity, version, and public-key fingerprint")
	}
	return secret, nil
}

// validateBootstrapScope checks the non-secret configuration before any key read.
func validateBootstrapScope(opts SeedOptions) error {
	b := opts.NstanceBootstrap
	if b == nil {
		return nil
	}
	if _, err := b.secret(opts); err != nil {
		return err
	}
	name := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	for _, value := range []string{b.Namespace.ValueString(), b.CAConfigMap.ValueString(), b.NonceSecret.ValueString()} {
		if len(value) > 63 || !name.MatchString(value) {
			return errors.New("bootstrap namespace and object names must be DNS labels of at most 63 characters")
		}
	}
	if b.ClusterID.ValueString() == "" || b.Tenant.ValueString() == "" || b.CABucket.ValueString() == "" {
		return errors.New("bootstrap requires cluster_id, tenant, and ca_bucket")
	}
	contents, err := os.ReadFile(opts.ClusterConfigPath)
	if err != nil {
		return errors.New("cannot read cluster configuration for bootstrap scope")
	}
	contents, err = hujson.Standardize(contents)
	if err != nil {
		return errors.New("invalid cluster configuration for bootstrap scope")
	}
	var cfg struct {
		Cluster struct {
			ID   string `json:"id"`
			Seed struct {
				Name string `json:"name"`
			} `json:"seed"`
			Providers []struct {
				Kind string `json:"kind"`
			} `json:"providers"`
		} `json:"cluster"`
	}
	if json.Unmarshal(contents, &cfg) != nil || cfg.Cluster.ID != b.ClusterID.ValueString() || !seeds.HasPlatformComponents(cfg.Cluster.Seed.Name) {
		return errors.New("operator bootstrap requires a matching cluster_id and a platform or agentic seed")
	}
	for _, p := range cfg.Cluster.Providers {
		if p.Kind == "aws" || p.Kind == "google" {
			return nil
		}
	}
	return errors.New("operator bootstrap requires an AWS or Google Cloud provider")
}

// addOperatorBootstrap inserts ordinary Kubernetes JSON records. Netsy receives
// readable snapshot bytes over TLS; the cloud store encrypts the object at rest.
func addOperatorBootstrap(snapshot []byte, b *nstanceBootstrapModel, ca []byte, token string) ([]byte, error) {
	records, err := datafile.ReadSnapshot(bytes.NewReader(snapshot))
	if err != nil {
		return nil, errors.New("cannot decode cluster-specific snapshot")
	}
	defer func() {
		for _, r := range records {
			clear(r.Value)
		}
	}()
	caKey := "/registry/configmaps/" + b.Namespace.ValueString() + "/" + b.CAConfigMap.ValueString()
	nonceKey := "/registry/secrets/" + b.Namespace.ValueString() + "/" + b.NonceSecret.ValueString()
	namespaceKey := "/registry/namespaces/" + b.Namespace.ValueString()
	namespaceExists := false
	enabled := false
	for _, r := range records {
		if string(r.Key) == namespaceKey {
			namespaceExists = true
		}
		if string(r.Key) == caKey || string(r.Key) == nonceKey {
			return nil, errors.New("generic seed already contains operator bootstrap objects; refusing to overwrite them")
		}
		if string(r.Key) == "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components" {
			var obj struct {
				Spec struct {
					Values struct {
						Platform struct {
							Components struct {
								Apps map[string]struct {
									Enabled bool `json:"enabled"`
								} `json:"apps"`
							} `json:"components"`
						} `json:"platform"`
					} `json:"values"`
				} `json:"spec"`
			}
			if json.Unmarshal(r.Value, &obj) == nil {
				enabled = obj.Spec.Values.Platform.Components.Apps["nstance-operator"].Enabled
			}
		}
	}
	if !enabled {
		return nil, errors.New("nstance-operator provider overlay is disabled; omit nstance_bootstrap")
	}
	if !namespaceExists {
		value, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]string{"name": b.Namespace.ValueString()}})
		if err != nil {
			return nil, errors.New("cannot encode operator namespace")
		}
		records = append(records, &datafile.Record{Key: []byte(namespaceKey), Value: value, Created: true, Version: 1})
	}
	objects := []struct {
		key, kind, name string
		data            any
	}{
		{caKey, "ConfigMap", b.CAConfigMap.ValueString(), map[string]string{"ca.crt": string(ca)}},
		{nonceKey, "Secret", b.NonceSecret.ValueString(), map[string][]byte{"nonce.jwt": []byte(token)}},
	}
	for _, obj := range objects {
		object := map[string]any{"apiVersion": "v1", "kind": obj.kind, "metadata": map[string]string{"name": obj.name, "namespace": b.Namespace.ValueString()}, "data": obj.data}
		if obj.kind == "Secret" {
			object["type"] = "Opaque"
		}
		value, err := json.Marshal(object)
		if err != nil {
			return nil, errors.New("cannot encode operator bootstrap object")
		}
		records = append(records, &datafile.Record{Key: []byte(obj.key), Value: value, Created: true, Version: 1})
	}
	// Seed interpolation can filter records. Normalize this fresh snapshot to the
	// contiguous revision contract required by Netsy's bootstrap consumer.
	for i, r := range records {
		r.Revision = int64(i + 1)
		r.CreateRevision = r.Revision
		r.PrevRevision = 0
	}
	var out bytes.Buffer
	if err := datafile.WriteSnapshot(&out, records, b.ClusterID.ValueString()); err != nil {
		clear(out.Bytes())
		return nil, errors.New("cannot encode operator bootstrap snapshot")
	}
	return out.Bytes(), nil
}

var errSnapshotAbsent = errors.New("snapshot absent")

// seedSnapshotMetadata identifies a stored snapshot and its completion and encryption status.
type seedSnapshotMetadata struct {
	Version, Completion string
	Encrypted           bool
}

// seedSnapshotStore separates secret-free metadata checks from payload access.
type seedSnapshotStore interface {
	Metadata(context.Context) (seedSnapshotMetadata, error)
	RequireEmpty(context.Context) error
	PublicCA(context.Context, *nstanceBootstrapModel) ([]byte, error)
	Upload(context.Context, []byte, string) error
}

// seedCompletion hashes non-secret seed inputs into a versioned completion marker.
func seedCompletion(opts SeedOptions) string {
	// Only a hash of non-secret input identity enters cloud object metadata.
	identity := []string{opts.ClusterConfigPath, opts.SeedPath, opts.ValuesFile, opts.ValuesContent}
	if b := opts.NstanceBootstrap; b != nil {
		identity = append(identity, b.Provider.ValueString(), b.SecretPrefix.ValueString(), b.SigningKeyID.ValueString(), b.SigningKeyVersion.ValueString(), b.SigningKeyFingerprint.ValueString(), b.ClusterID.ValueString(), b.Tenant.ValueString(), b.Namespace.ValueString(), b.CABucket.ValueString(), b.CAPrefix.ValueString(), b.CAConfigMap.ValueString(), b.NonceSecret.ValueString())
	}
	encoded, _ := json.Marshal(identity)
	sum := sha256.Sum256(encoded)
	return "v1-" + hex.EncodeToString(sum[:])
}

// completedSeed checks whether stored metadata permits adopting the existing snapshot.
func completedSeed(ctx context.Context, opts SeedOptions, store seedSnapshotStore) (seedSnapshotMetadata, bool, error) {
	meta, err := store.Metadata(ctx)
	if errors.Is(err, errSnapshotAbsent) {
		return meta, false, nil
	}
	if err != nil {
		return meta, false, err
	}
	if meta.Completion != seedCompletion(opts) || opts.NstanceBootstrap != nil && !meta.Encrypted {
		return meta, false, errors.New("existing bootstrap snapshot does not match the completed seed scope or encryption contract; refusing to overwrite it")
	}
	return meta, true, nil
}

// waitPublicCA polls for a current CA certificate until it appears or the context ends.
func waitPublicCA(ctx context.Context, read func(context.Context) ([]byte, error), interval time.Duration) ([]byte, error) {
	for {
		ca, err := read(ctx)
		if err == nil {
			block, rest := pem.Decode(ca)
			if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
				return nil, errors.New("public Nstance ca.crt must contain one PEM CA certificate")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			now := time.Now()
			if err != nil || !cert.IsCA || now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
				return nil, errors.New("public Nstance ca.crt is not a valid current CA certificate")
			}
			return ca, nil
		}
		if !errors.Is(err, errSnapshotAbsent) {
			return nil, err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.New("timed out or cancelled waiting for Nstance public ca.crt")
		case <-timer.C:
		}
	}
}

// readPublicCA limits public certificate downloads without touching secret values.
func readPublicCA(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return nil, fmt.Errorf("cannot read Nstance public CA certificate")
	}
	return data, nil
}
