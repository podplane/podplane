// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package userdata

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
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

	// EnableSSM controls AWS SSM Agent installation.
	EnableSSM bool

	// AWSAccountID and GoogleProjectID are dedicated fields because they are
	// provider inputs, not Nstance mutable env vars.
	AWSAccountID    string
	GoogleProjectID string

	// Nonce is a dedicated field because it is Nstance first-boot bootstrap data,
	// not a value vmconfig should receive later through mutable.env.
	Nonce string

	// ServerEnv supplies nst boot-time settings, immutable for that VM.
	ServerEnv map[string]string

	// ActivityPorts selects the TCP ports monitored by Kubernetes nodes.
	ActivityPorts string

	// NATIPv4SourceCIDRs supplies the NAT VM's immutable source allowlist.
	NATIPv4SourceCIDRs string

	// The following fields are only used for local VMs and is not used
	// when rendering user-data for real clusters
	Local LocalData
}

// ClusterData supplies the cluster identity and CA certificate to user-data.
type ClusterData struct {
	ID     string
	CACert string
}

// ProviderData describes the VM's infrastructure provider and location.
type ProviderData struct {
	Kind   string
	Region string
	Zone   string
}

// InstanceData supplies the VM identity and instance type to user-data.
type InstanceData struct {
	ID   string
	Type string
}

// ServerData supplies the Nstance registration and agent server addresses.
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

// ManifestFilter selects dependencies that apply to this VM's provider.
func (v *TemplateVars) ManifestFilter() deps.ItemFilter {
	return deps.ItemFilter{Providers: []string{v.Provider.Kind}}
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
	// envName constrains keys inserted as shell assignment declarations.
	envName := regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	for name, value := range v.ServerEnv {
		if !envName.MatchString(name) {
			return fmt.Errorf("ServerEnv key %q is not an environment variable name", name)
		}
		if strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("ServerEnv.%s contains a NUL or newline", name)
		}
	}
	if v.Manifest.VMConfig.Kind != "nst" && len(v.ServerEnv) > 0 {
		return fmt.Errorf("ServerEnv is only supported for nst")
	}
	if strings.ContainsAny(v.ImmutableSSHAuthorizedKeys, "\x00\r") {
		return fmt.Errorf("ImmutableSSHAuthorizedKeys contains a NUL or carriage return")
	}
	if v.Provider.Kind == "" {
		return fmt.Errorf("Provider.Kind failed validation on 'required' validator")
	}
	if !isTemplateValue(v.Provider.Kind) && !map[string]bool{"local": true, "aws": true, "google": true, "proxmox": true}[v.Provider.Kind] {
		return fmt.Errorf("Provider.Kind failed validation on 'oneof' validator")
	}
	if v.Instance.ID == "" && v.Manifest.VMConfig.Kind != "nst" {
		return fmt.Errorf("Instance.ID failed validation on 'required' validator")
	}
	if v.Cluster.ID == "" && v.Manifest.VMConfig.Kind != "nst" {
		return fmt.Errorf("Cluster.ID failed validation on 'required' validator")
	}
	return nil
}

// Render validates the template variables and renders the user-data script.
func (v *TemplateVars) Render() (string, error) {
	if err := v.Validate(); err != nil {
		return "", err
	}
	tmpl, err := template.New("userdata").Funcs(template.FuncMap{
		"quoteEnv": quoteEnvValue,
	}).Option("missingkey=zero").Parse(userdataTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse userdata template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, v); err != nil {
		return "", fmt.Errorf("failed to render userdata template: %w", err)
	}
	return sb.String(), nil
}

// quoteEnvValue quotes a value using vmconfig's env-file shell format.
func quoteEnvValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// renderManifest renders the canonical userdata template from a parsed manifest.
func renderManifest(manifest *deps.Manifest, opts Options) (string, error) {
	vars := TemplateVars{
		Manifest:                   manifest,
		DepsMirrorURL:              opts.DepsMirrorURL,
		Provider:                   ProviderData{Kind: opts.ProviderKind},
		AWSAccountID:               opts.AWSAccountID,
		GoogleProjectID:            opts.GoogleProjectID,
		ImmutableSSHAuthorizedKeys: opts.ImmutableSSHAuthorizedKeys,
		EnableSSM:                  opts.EnableSSM,
		ServerEnv:                  opts.ServerEnv,
	}
	if manifest.VMConfig.Kind == "nst" {
		// NST is rendered by Terraform, without Nstance agent interpolation.
		return vars.Render()
	}
	vars.Cluster = ClusterData{ID: "{{ .Cluster.ID }}", CACert: "{{ .Cluster.CACert }}"}
	vars.Provider.Region = "{{ .Provider.Region }}"
	vars.Provider.Zone = "{{ .Provider.Zone }}"
	vars.Instance = InstanceData{ID: "{{ .Instance.ID }}", Type: "{{ .Instance.Type }}"}
	vars.Server = ServerData{
		RegistrationAddr: "{{ .Server.RegistrationAddr }}",
		AgentAddr:        "{{ .Server.AgentAddr }}",
	}
	vars.Nonce = "{{ .Nonce }}"
	if opts.ProviderKind != "local" {
		switch manifest.VMConfig.Kind {
		case "knc", "knd":
			vars.ActivityPorts = "{{ .Vars.NSTANCE_EBPF_PORTS }}"
		case "nat":
			vars.NATIPv4SourceCIDRs = "{{ .Vars.NAT_IPV4_SOURCE_CIDRS }}"
		}
	}
	return vars.Render()
}

// Options contains values used to render Podplane userdata.
type Options struct {
	// DepsMirrorURL overrides upstream dependency URLs with mirror-relative URLs.
	DepsMirrorURL string
	// ProviderKind selects provider-specific dependencies and bootstrap behavior.
	ProviderKind string
	// AWSAccountID supplies the AWS account identity to vmconfig.
	AWSAccountID string
	// GoogleProjectID supplies the Google project identity to vmconfig.
	GoogleProjectID string
	// ImmutableSSHAuthorizedKeys installs first-boot debugging keys.
	ImmutableSSHAuthorizedKeys string
	// EnableSSM installs and starts AWS SSM Agent.
	EnableSSM bool
	// ServerEnv supplies non-sensitive nst settings written to mutable.env once
	// at provisioning. Changes require replacing the VM.
	ServerEnv map[string]string
}

// Render renders userdata from an explicitly pinned vmconfig manifest JSON
// document. It performs no network or file access. Agent-managed kinds retain
// their nstance-server Go-template placeholders; nst is ready for cloud-init.
func Render(manifestJSON []byte, opts Options) (string, error) {
	var manifest deps.Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return "", fmt.Errorf("parse vmconfig manifest: %w", err)
	}
	if err := validateManifest(&manifest, opts.ProviderKind); err != nil {
		return "", err
	}
	return renderManifest(&manifest, opts)
}

// validateManifest checks the pinned manifest fields consumed while
// rendering production userdata.
func validateManifest(manifest *deps.Manifest, providerKind string) error {
	vmconfig := manifest.VMConfig
	for _, field := range []struct {
		name  string
		value string
	}{
		{"vmconfig.version", vmconfig.Version},
		{"vmconfig.kind", vmconfig.Kind},
		{"vmconfig.os.name", vmconfig.OS.Name},
		{"vmconfig.os.arch", vmconfig.OS.Arch},
	} {
		if field.value == "" {
			return fmt.Errorf("vmconfig manifest %s is required", field.name)
		}
	}
	for _, item := range manifest.InstallItems(deps.ItemFilter{Providers: []string{providerKind}}) {
		if item.Dep.Version == "" {
			return fmt.Errorf("vmconfig dependency %s version is required", item.Name)
		}
		if item.Dep.URL == "" {
			return fmt.Errorf("vmconfig dependency %s URL is required", item.Name)
		}
		if _, _, err := item.Dep.ParseDigest(); err != nil {
			return fmt.Errorf("vmconfig dependency %s: %w", item.Name, err)
		}
	}
	return nil
}
