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

	"github.com/go-playground/validator/v10"
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
	Vars     MutableVars

	// AWSAccountID and GoogleProjectID are dedicated fields because they are
	// tf provider inputs, not Nstance mutable env vars pushed through Vars.
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

// MutableVars are the variables exposed to the canonical Nstance-style
// userdata template and rendered into mutable.env.
type MutableVars map[string]string

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

// RenderMutableEnv renders the subset of user-data env that vmconfig's
// update-mutable-env.sh accepts for post-boot updates.
func RenderMutableEnv(vars MutableVars) string {
	lines := []string{
		"SSH_AUTHORIZED_KEY=" + QuoteEnvValue(vars["SSH_AUTHORIZED_KEY"]),
		"KUBE_API_PUBLIC_HOSTNAME=" + QuoteEnvValue(vars["KUBE_API_PUBLIC_HOSTNAME"]),
		"KUBE_API_PORT=" + QuoteEnvValue(vars["KUBE_API_PORT"]),
		"KUBE_API_INTERNAL_LB_HOSTNAME=" + QuoteEnvValue(vars["KUBE_API_INTERNAL_LB_HOSTNAME"]),
		"NSTANCE_SERVER_REGISTRATION_ADDR=" + QuoteEnvValue(vars["NSTANCE_SERVER_REGISTRATION_ADDR"]),
		"NSTANCE_SERVER_AGENT_ADDR=" + QuoteEnvValue(vars["NSTANCE_SERVER_AGENT_ADDR"]),
		"KUBE_API_ETCD_SERVERS=" + QuoteEnvValue(vars["KUBE_API_ETCD_SERVERS"]),
		"OIDC_ISSUER=" + QuoteEnvValue(vars["OIDC_ISSUER"]),
		"OIDC_CUSTOM_CA=" + QuoteEnvValue(vars["OIDC_CUSTOM_CA"]),
		"OIDC_CA_FILE=" + QuoteEnvValue(vars["OIDC_CA_FILE"]),
		"KUBE_LOG_LEVEL=" + QuoteEnvValue(vars["KUBE_LOG_LEVEL"]),
		"NETSY_BUCKET=" + QuoteEnvValue(vars["NETSY_BUCKET"]),
		"NETSY_ENDPOINT=" + QuoteEnvValue(vars["NETSY_ENDPOINT"]),
		"NETSY_REGION=" + QuoteEnvValue(vars["NETSY_REGION"]),
		"NETSY_ASSUME_ROLE=" + QuoteEnvValue(vars["NETSY_ASSUME_ROLE"]),
		"NETSY_ACCESS_KEY_ID=" + QuoteEnvValue(vars["NETSY_ACCESS_KEY_ID"]),
		"NETSY_SECRET_ACCESS_KEY=" + QuoteEnvValue(vars["NETSY_SECRET_ACCESS_KEY"]),
		"TELEMETRY_ENABLED=" + QuoteEnvValue(vars["TELEMETRY_ENABLED"]),
		"TELEMETRY_LOG_SERVICES=" + QuoteEnvValue(vars["TELEMETRY_LOG_SERVICES"]),
		"TELEMETRY_LOG_CLOUDINIT=" + QuoteEnvValue(vars["TELEMETRY_LOG_CLOUDINIT"]),
		"TELEMETRY_S3_BUCKET=" + QuoteEnvValue(vars["TELEMETRY_S3_BUCKET"]),
		"TELEMETRY_S3_ENDPOINT=" + QuoteEnvValue(vars["TELEMETRY_S3_ENDPOINT"]),
		"TELEMETRY_S3_REGION=" + QuoteEnvValue(vars["TELEMETRY_S3_REGION"]),
		"TELEMETRY_S3_ASSUME_ROLE=" + QuoteEnvValue(vars["TELEMETRY_S3_ASSUME_ROLE"]),
		"TELEMETRY_S3_ACCESS_KEY_ID=" + QuoteEnvValue(vars["TELEMETRY_S3_ACCESS_KEY_ID"]),
		"TELEMETRY_S3_SECRET_ACCESS_KEY=" + QuoteEnvValue(vars["TELEMETRY_S3_SECRET_ACCESS_KEY"]),
		"TELEMETRY_OTLP_ENDPOINT=" + QuoteEnvValue(vars["TELEMETRY_OTLP_ENDPOINT"]),
		"REGISTRY_ENABLED=" + QuoteEnvValue(vars["REGISTRY_ENABLED"]),
		"REGISTRY_BUCKET=" + QuoteEnvValue(vars["REGISTRY_BUCKET"]),
		"REGISTRY_HOSTNAME=" + QuoteEnvValue(vars["REGISTRY_HOSTNAME"]),
		"REGISTRY_ENDPOINT=" + QuoteEnvValue(vars["REGISTRY_ENDPOINT"]),
		"REGISTRY_REGION=" + QuoteEnvValue(vars["REGISTRY_REGION"]),
		"REGISTRY_ASSUME_ROLE=" + QuoteEnvValue(vars["REGISTRY_ASSUME_ROLE"]),
		"REGISTRY_ACCESS_KEY_ID=" + QuoteEnvValue(vars["REGISTRY_ACCESS_KEY_ID"]),
		"REGISTRY_SECRET_ACCESS_KEY=" + QuoteEnvValue(vars["REGISTRY_SECRET_ACCESS_KEY"]),
		"AWS_S3_USE_PATH_STYLE=" + QuoteEnvValue(vars["AWS_S3_USE_PATH_STYLE"]),
	}
	return strings.Join(lines, "\n") + "\n"
}

// QuoteEnvValue quotes a value using the same single-quote format as vmconfig's
// shell helpers use for env files.
func QuoteEnvValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// ApplyDefaults populates derived values (e.g. cluster-prefixed bucket names)
// when the caller has not set them explicitly.
func (v *TemplateVars) ApplyDefaults() {
	if v.Vars == nil {
		v.Vars = MutableVars{}
	}
	if v.Vars["KUBE_LOG_LEVEL"] == "" {
		v.Vars["KUBE_LOG_LEVEL"] = "2"
	}
	if v.Vars["KUBE_API_PORT"] == "" {
		v.Vars["KUBE_API_PORT"] = "6443"
	}
	if v.Vars["TELEMETRY_ENABLED"] == "" {
		v.Vars["TELEMETRY_ENABLED"] = "false"
	}
	if v.Vars["TELEMETRY_LOG_CLOUDINIT"] == "" {
		v.Vars["TELEMETRY_LOG_CLOUDINIT"] = "true"
	}
	if v.Vars["REGISTRY_ENABLED"] == "" {
		v.Vars["REGISTRY_ENABLED"] = "true"
	}
	if v.Cluster.ID != "" {
		if v.Vars["NETSY_BUCKET"] == "" {
			v.Vars["NETSY_BUCKET"] = fmt.Sprintf("%s-netsy", v.Cluster.ID)
		}
		if v.Vars["REGISTRY_BUCKET"] == "" {
			v.Vars["REGISTRY_BUCKET"] = fmt.Sprintf("%s-registry", v.Cluster.ID)
		}
		if v.Vars["TELEMETRY_S3_BUCKET"] == "" {
			v.Vars["TELEMETRY_S3_BUCKET"] = fmt.Sprintf("%s-telemetry", v.Cluster.ID)
		}
	}
}

// validate is package-scoped so struct tags are cached across calls.
var validate = validator.New(validator.WithRequiredStructEnabled())

var serviceListRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(,[a-z0-9][a-z0-9-]*)*$`)

func init() {
	if err := validate.RegisterValidation("uintstr", validateUintString); err != nil {
		panic(err)
	}
	if err := validate.RegisterValidation("portstr", validatePortString); err != nil {
		panic(err)
	}
	if err := validate.RegisterValidation("boolstr", validateBoolString); err != nil {
		panic(err)
	}
	if err := validate.RegisterValidation("service_list", validateServiceList); err != nil {
		panic(err)
	}
}

func validateUintString(fl validator.FieldLevel) bool {
	_, err := strconv.ParseUint(fl.Field().String(), 10, 64)
	return err == nil
}

func validatePortString(fl validator.FieldLevel) bool {
	port, err := strconv.ParseUint(fl.Field().String(), 10, 16)
	return err == nil && port > 0
}

func validateBoolString(fl validator.FieldLevel) bool {
	switch fl.Field().String() {
	case "true", "false":
		return true
	default:
		return false
	}
}

func validateServiceList(fl validator.FieldLevel) bool {
	return serviceListRE.MatchString(fl.Field().String())
}

// Validate checks the TemplateVars are populated correctly enough to render.
func (v *TemplateVars) Validate() error {
	if v.Manifest == nil {
		return fmt.Errorf("manifest failed validation on 'required' validator")
	}
	if err := validateCanonicalValues(v); err != nil {
		return err
	}
	return nil
}

// validateCanonicalValues checks the canonical Nstance-shaped userdata inputs.
func validateCanonicalValues(v *TemplateVars) error {
	check := func(name string, value string) error {
		if strings.ContainsAny(value, "'\n\r") {
			return fmt.Errorf("%s failed validation on 'envstr' validator", name)
		}
		return nil
	}
	isTemplateValue := func(value string) bool {
		return strings.Contains(value, "{{")
	}
	validUintString := func(value string) bool {
		_, err := strconv.ParseUint(value, 10, 64)
		return err == nil
	}
	validPortString := func(value string) bool {
		port, err := strconv.ParseUint(value, 10, 16)
		return err == nil && port > 0
	}
	validBoolString := func(value string) bool {
		switch value {
		case "true", "false":
			return true
		default:
			return false
		}
	}
	values := map[string]string{
		"Cluster.ID":              v.Cluster.ID,
		"Provider.Kind":           v.Provider.Kind,
		"Provider.Region":         v.Provider.Region,
		"Provider.Zone":           v.Provider.Zone,
		"Instance.ID":             v.Instance.ID,
		"Instance.Type":           v.Instance.Type,
		"Server.RegistrationAddr": v.Server.RegistrationAddr,
		"Server.AgentAddr":        v.Server.AgentAddr,
		"AWSAccountID":            v.AWSAccountID,
		"GoogleProjectID":         v.GoogleProjectID,
	}
	for name, value := range values {
		if err := check(name, value); err != nil {
			return err
		}
	}
	for key, value := range v.Vars {
		if err := check("Vars."+key, value); err != nil {
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
	if !isTemplateValue(v.Vars["KUBE_LOG_LEVEL"]) && !validUintString(v.Vars["KUBE_LOG_LEVEL"]) {
		return fmt.Errorf("Vars.KUBE_LOG_LEVEL failed validation on 'uintstr' validator")
	}
	if !isTemplateValue(v.Vars["KUBE_API_PORT"]) && !validPortString(v.Vars["KUBE_API_PORT"]) {
		return fmt.Errorf("Vars.KUBE_API_PORT failed validation on 'portstr' validator")
	}
	if !isTemplateValue(v.Vars["TELEMETRY_LOG_CLOUDINIT"]) && !validBoolString(v.Vars["TELEMETRY_LOG_CLOUDINIT"]) {
		return fmt.Errorf("Vars.TELEMETRY_LOG_CLOUDINIT failed validation on 'boolstr' validator")
	}
	if !isTemplateValue(v.Vars["TELEMETRY_ENABLED"]) && !validBoolString(v.Vars["TELEMETRY_ENABLED"]) {
		return fmt.Errorf("Vars.TELEMETRY_ENABLED failed validation on 'boolstr' validator")
	}
	if !isTemplateValue(v.Vars["REGISTRY_ENABLED"]) && !validBoolString(v.Vars["REGISTRY_ENABLED"]) {
		return fmt.Errorf("Vars.REGISTRY_ENABLED failed validation on 'boolstr' validator")
	}
	if v.Vars["TELEMETRY_LOG_SERVICES"] != "" && !isTemplateValue(v.Vars["TELEMETRY_LOG_SERVICES"]) && !serviceListRE.MatchString(v.Vars["TELEMETRY_LOG_SERVICES"]) {
		return fmt.Errorf("Vars.TELEMETRY_LOG_SERVICES failed validation on 'service_list' validator")
	}
	if v.Vars["REGISTRY_ENABLED"] == "true" && v.Vars["REGISTRY_HOSTNAME"] == "" {
		return fmt.Errorf("Vars.REGISTRY_HOSTNAME failed validation on 'required_if_registry_enabled' validator")
	}
	return nil
}

// Render produces the rendered user-data script.
func (v *TemplateVars) Render() (string, error) {
	v.ApplyDefaults()
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
func SourceForNstance(manifest *deps.Manifest, depsMirrorURL string, providerKind string, awsAccountID string, googleProjectID string) (string, error) {
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
		AWSAccountID:    awsAccountID,
		GoogleProjectID: googleProjectID,
		Vars: map[string]string{
			"SSH_AUTHORIZED_KEY":             "{{ .Vars.SSH_AUTHORIZED_KEY }}",
			"OIDC_ISSUER":                    "{{ .Vars.OIDC_ISSUER }}",
			"OIDC_CUSTOM_CA":                 "{{ .Vars.OIDC_CUSTOM_CA }}",
			"OIDC_CA_FILE":                   "{{ .Vars.OIDC_CA_FILE }}",
			"KUBE_LOG_LEVEL":                 "{{ .Vars.KUBE_LOG_LEVEL }}",
			"KUBE_API_PUBLIC_HOSTNAME":       "{{ .Vars.KUBE_API_PUBLIC_HOSTNAME }}",
			"KUBE_API_PORT":                  "{{ .Vars.KUBE_API_PORT }}",
			"KUBE_API_INTERNAL_LB_HOSTNAME":  "{{ .Vars.KUBE_API_INTERNAL_LB_HOSTNAME }}",
			"KUBE_API_ETCD_SERVERS":          "{{ .Vars.KUBE_API_ETCD_SERVERS }}",
			"NETSY_BUCKET":                   "{{ .Vars.NETSY_BUCKET }}",
			"NETSY_ENDPOINT":                 "{{ .Vars.NETSY_ENDPOINT }}",
			"NETSY_ASSUME_ROLE":              "{{ .Vars.NETSY_ASSUME_ROLE }}",
			"NETSY_REGION":                   "{{ .Vars.NETSY_REGION }}",
			"NETSY_ACCESS_KEY_ID":            "{{ .Vars.NETSY_ACCESS_KEY_ID }}",
			"NETSY_SECRET_ACCESS_KEY":        "{{ .Vars.NETSY_SECRET_ACCESS_KEY }}",
			"TELEMETRY_ENABLED":              "{{ .Vars.TELEMETRY_ENABLED }}",
			"TELEMETRY_S3_BUCKET":            "{{ .Vars.TELEMETRY_S3_BUCKET }}",
			"TELEMETRY_S3_ENDPOINT":          "{{ .Vars.TELEMETRY_S3_ENDPOINT }}",
			"TELEMETRY_S3_REGION":            "{{ .Vars.TELEMETRY_S3_REGION }}",
			"TELEMETRY_S3_ASSUME_ROLE":       "{{ .Vars.TELEMETRY_S3_ASSUME_ROLE }}",
			"TELEMETRY_LOG_SERVICES":         "{{ .Vars.TELEMETRY_LOG_SERVICES }}",
			"TELEMETRY_LOG_CLOUDINIT":        "{{ .Vars.TELEMETRY_LOG_CLOUDINIT }}",
			"TELEMETRY_S3_ACCESS_KEY_ID":     "{{ .Vars.TELEMETRY_S3_ACCESS_KEY_ID }}",
			"TELEMETRY_S3_SECRET_ACCESS_KEY": "{{ .Vars.TELEMETRY_S3_SECRET_ACCESS_KEY }}",
			"TELEMETRY_OTLP_ENDPOINT":        "{{ .Vars.TELEMETRY_OTLP_ENDPOINT }}",
			"REGISTRY_ENABLED":               "{{ .Vars.REGISTRY_ENABLED }}",
			"REGISTRY_BUCKET":                "{{ .Vars.REGISTRY_BUCKET }}",
			"REGISTRY_HOSTNAME":              "{{ .Vars.REGISTRY_HOSTNAME }}",
			"REGISTRY_ENDPOINT":              "{{ .Vars.REGISTRY_ENDPOINT }}",
			"REGISTRY_REGION":                "{{ .Vars.REGISTRY_REGION }}",
			"REGISTRY_ASSUME_ROLE":           "{{ .Vars.REGISTRY_ASSUME_ROLE }}",
			"REGISTRY_ACCESS_KEY_ID":         "{{ .Vars.REGISTRY_ACCESS_KEY_ID }}",
			"REGISTRY_SECRET_ACCESS_KEY":     "{{ .Vars.REGISTRY_SECRET_ACCESS_KEY }}",
			"AWS_S3_USE_PATH_STYLE":          "{{ .Vars.AWS_S3_USE_PATH_STYLE }}",
		},
		Nonce: "{{ .Nonce }}",
	}
	return vars.Render()
}
