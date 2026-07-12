// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package userdata

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/podplane/podplane/internal/deps"
)

//go:embed user-data.sh
var userdataTemplate string

// TemplateVars are the template variables consumed by user-data.sh.
type TemplateVars struct {
	// Manifest is the resolved vmconfig manifest for this VM.
	Manifest *deps.Manifest `validate:"required"`

	// DepsMirrorURL, when set, overrides all upstream dependency URLs with
	// mirror-relative paths (<base>/<name>/<version>/<filename>). Used for
	// local development and air-gapped installs.
	DepsMirrorURL string `validate:"omitempty,url"`

	// The following fields map to the same structure nstance-server uses
	// in production for interpolating data into production userdata
	Cluster  ClusterData
	Provider ProviderData
	Instance InstanceData
	Server   ServerData

	// ImmutableSSHAuthorizedKeys is written only by user-data and changing it
	// changes the Nstance instance configuration hash.
	ImmutableSSHAuthorizedKeys string

	// AWSAccountID and GoogleProjectID are dedicated fields because they are
	// provider inputs, not Nstance mutable env vars.
	AWSAccountID    string
	GoogleProjectID string

	// Nonce is a dedicated field because it is Nstance first-boot bootstrap data,
	// not a value vmconfig should receive later through mutable.env.
	Nonce string

	// The following fields are only used for local VMs and is not used
	// when rendering user-data for real clusters
	Local LocalData
}

type ClusterData struct {
	ID     string
	CACert string
}

type ProviderData struct {
	Kind   string
	Region string
	Zone   string
}

type InstanceData struct {
	ID   string
	Type string
}

type ServerData struct {
	RegistrationAddr string
	AgentAddr        string
}

// LocalData contains values only rendered for local VMs.
type LocalData struct {
	VMForwardPortToLocalServerHTTPS int
	LocalServerHostFromVM           string
	LocalServerHTTPSPort            string
}

// MutableVars are the variables delivered by nstance-agent through mutable.env.
type MutableVars map[string]string

// mutableEnvKeys defines the vmconfig mutable.env contract. Keep this in sync
// with vmconfig's default mutable.env file.
var mutableEnvKeys = []string{
	"SSH_AUTHORIZED_KEYS",
	"TELEMETRY_ENABLED",
	"TELEMETRY_LOG_CLOUDINIT",
	"TELEMETRY_LOG_SERVICES",
	"TELEMETRY_OTLP_ENDPOINT",
	"TELEMETRY_S3_BUCKET",
	"TELEMETRY_S3_REGION",
	"TELEMETRY_S3_ENDPOINT",
	"TELEMETRY_S3_ACCESS_KEY_ID",
	"TELEMETRY_S3_SECRET_ACCESS_KEY",
	"TELEMETRY_S3_ASSUME_ROLE",
	"OIDC_ISSUER",
	"OIDC_CA_CERT",
	"KUBE_API_ETCD_SERVERS",
	"KUBE_API_PUBLIC_HOSTNAME",
	"KUBE_API_INTERNAL_LB_HOSTNAME",
	"KUBE_API_PORT",
	"KUBE_LOG_LEVEL",
	"AWS_S3_USE_PATH_STYLE",
	"NETSY_BUCKET",
	"NETSY_REGION",
	"NETSY_ENDPOINT",
	"NETSY_ACCESS_KEY_ID",
	"NETSY_SECRET_ACCESS_KEY",
	"NETSY_ASSUME_ROLE",
	"REGISTRY_ENABLED",
	"REGISTRY_BUCKET",
	"REGISTRY_REGION",
	"REGISTRY_ENDPOINT",
	"REGISTRY_ACCESS_KEY_ID",
	"REGISTRY_SECRET_ACCESS_KEY",
	"REGISTRY_ASSUME_ROLE",
	"REGISTRY_HOSTNAME",
}

// ManifestFilter selects dependencies that apply to this VM's provider.
func (v *TemplateVars) ManifestFilter() deps.ItemFilter {
	return deps.ItemFilter{Providers: []string{v.Provider.Kind}}
}

// SetObjectStorageEndpoint sets all component object storage endpoints to the
// same value. Use direct field assignment when components use different stores.
func (v MutableVars) SetObjectStorageEndpoint(endpoint string) {
	v["NETSY_ENDPOINT"] = endpoint
	v["TELEMETRY_S3_ENDPOINT"] = endpoint
	v["REGISTRY_ENDPOINT"] = endpoint
}

// SetObjectStorageRegion sets all component object storage regions to the same
// value. Use direct field assignment when components use different stores.
func (v MutableVars) SetObjectStorageRegion(region string) {
	v["NETSY_REGION"] = region
	v["TELEMETRY_S3_REGION"] = region
	v["REGISTRY_REGION"] = region
}

// RenderMutableEnv validates and renders the mutable.env file delivered to vmconfig.
func RenderMutableEnv(vars MutableVars) (string, error) {
	if err := vars.Validate(); err != nil {
		return "", err
	}
	lines := make([]string, 0, len(mutableEnvKeys))
	for _, key := range mutableEnvKeys {
		lines = append(lines, key+"="+QuoteEnvValue(vars[key]))
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// QuoteEnvValue quotes a value using the same single-quote format as vmconfig's
// shell helpers use for env files.
func QuoteEnvValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// ApplyDefaults populates mutable var defaults and values derived from clusterID.
func (v MutableVars) ApplyDefaults(clusterID string) {
	defaults := map[string]string{
		"KUBE_LOG_LEVEL":          "2",
		"KUBE_API_PORT":           "6443",
		"TELEMETRY_ENABLED":       "false",
		"TELEMETRY_LOG_CLOUDINIT": "true",
		"REGISTRY_ENABLED":        "true",
	}
	for key, value := range defaults {
		if v[key] == "" {
			v[key] = value
		}
	}
	if clusterID == "" {
		return
	}
	for key, suffix := range map[string]string{
		"NETSY_BUCKET":        "netsy",
		"REGISTRY_BUCKET":     "registry",
		"TELEMETRY_S3_BUCKET": "telemetry",
	} {
		if v[key] == "" {
			v[key] = fmt.Sprintf("%s-%s", clusterID, suffix)
		}
	}
}

var serviceListRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(,[a-z0-9][a-z0-9-]*)*$`)

// Validate checks values delivered through mutable.env.
func (v MutableVars) Validate() error {
	for key, value := range v {
		if strings.ContainsAny(value, "'\n\r") {
			return fmt.Errorf("mutable environment variable %s contains a quote or newline", key)
		}
	}
	if _, err := strconv.ParseUint(v["KUBE_LOG_LEVEL"], 10, 64); err != nil {
		return fmt.Errorf("KUBE_LOG_LEVEL must be a non-negative integer")
	}
	port, err := strconv.ParseUint(v["KUBE_API_PORT"], 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("KUBE_API_PORT must be an integer between 1 and 65535")
	}
	for _, key := range []string{"TELEMETRY_LOG_CLOUDINIT", "TELEMETRY_ENABLED", "REGISTRY_ENABLED"} {
		if v[key] != "true" && v[key] != "false" {
			return fmt.Errorf("%s must be either true or false", key)
		}
	}
	if services := v["TELEMETRY_LOG_SERVICES"]; services != "" && !serviceListRE.MatchString(services) {
		return fmt.Errorf("TELEMETRY_LOG_SERVICES must be a comma-separated list of lowercase service names")
	}
	if v["REGISTRY_ENABLED"] == "true" && v["REGISTRY_HOSTNAME"] == "" {
		return fmt.Errorf("REGISTRY_HOSTNAME is required when REGISTRY_ENABLED is true")
	}
	return nil
}

// Validate checks the TemplateVars are populated correctly enough to render.
func (v *TemplateVars) Validate() error {
	if v.Manifest == nil {
		return fmt.Errorf("manifest failed validation on 'required' validator")
	}
	if err := validateTemplateValues(v); err != nil {
		return err
	}
	return nil
}

// validateTemplateValues checks values interpolated into user-data.
func validateTemplateValues(v *TemplateVars) error {
	check := func(name string, value string) error {
		if strings.ContainsAny(value, "'\n\r") {
			return fmt.Errorf("%s failed validation on 'envstr' validator", name)
		}
		return nil
	}
	isTemplateValue := func(value string) bool {
		return strings.Contains(value, "{{")
	}
	values := map[string]string{
		"Cluster.ID":                 v.Cluster.ID,
		"Provider.Kind":              v.Provider.Kind,
		"Provider.Region":            v.Provider.Region,
		"Provider.Zone":              v.Provider.Zone,
		"Instance.ID":                v.Instance.ID,
		"Instance.Type":              v.Instance.Type,
		"Server.RegistrationAddr":    v.Server.RegistrationAddr,
		"Server.AgentAddr":           v.Server.AgentAddr,
		"AWSAccountID":               v.AWSAccountID,
		"GoogleProjectID":            v.GoogleProjectID,
		"ImmutableSSHAuthorizedKeys": v.ImmutableSSHAuthorizedKeys,
	}
	for name, value := range values {
		if err := check(name, value); err != nil {
			return err
		}
	}
	if v.Provider.Kind == "" {
		return fmt.Errorf("Provider.Kind failed validation on 'required' validator")
	}
	if !isTemplateValue(v.Provider.Kind) && !map[string]bool{"local": true, "aws": true, "google": true, "proxmox": true}[v.Provider.Kind] {
		return fmt.Errorf("Provider.Kind failed validation on 'oneof' validator")
	}
	if v.Instance.ID == "" {
		return fmt.Errorf("Instance.ID failed validation on 'required' validator")
	}
	if v.Cluster.ID == "" {
		return fmt.Errorf("Cluster.ID failed validation on 'required' validator")
	}
	return nil
}

// Render produces the rendered user-data script.
func (v *TemplateVars) Render() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	tmpl, err := template.New("userdata").Option("missingkey=zero").Parse(userdataTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse userdata template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, v); err != nil {
		return "", fmt.Errorf("failed to render userdata template: %w", err)
	}
	return sb.String(), nil
}

// SourceForNstance renders the canonical userdata template into the template
// source nstance-server renders later for each VM.
func SourceForNstance(manifest *deps.Manifest, depsMirrorURL string, providerKind string, awsAccountID string, googleProjectID string, immutableSSHAuthorizedKeys string) (string, error) {
	vars := TemplateVars{
		Manifest:      manifest,
		DepsMirrorURL: depsMirrorURL,
		Cluster: ClusterData{
			ID:     "{{ .Cluster.ID }}",
			CACert: "{{ .Cluster.CACert }}",
		},
		Provider: ProviderData{
			Kind:   providerKind,
			Region: "{{ .Provider.Region }}",
			Zone:   "{{ .Provider.Zone }}",
		},
		Instance: InstanceData{
			ID:   "{{ .Instance.ID }}",
			Type: "{{ .Instance.Type }}",
		},
		Server: ServerData{
			RegistrationAddr: "{{ .Server.RegistrationAddr }}",
			AgentAddr:        "{{ .Server.AgentAddr }}",
		},
		AWSAccountID:               awsAccountID,
		GoogleProjectID:            googleProjectID,
		ImmutableSSHAuthorizedKeys: immutableSSHAuthorizedKeys,
		Nonce:                      "{{ .Nonce }}",
	}
	return vars.Render()
}
