// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/pkg/seeds"
)

// SelectCloudProvider asks which cloud provider should host the new cluster.
// AWS is selected initially because it is the first list item.
func SelectCloudProvider() (string, bool, error) {
	return tui.SelectList("Select cloud provider", "Which cloud provider?", cloudProviderItems())
}

func cloudProviderItems() []list.Item {
	return []list.Item{
		tui.Item{Key: "aws", Label: "Amazon Web Services (AWS)"},
		tui.Item{Key: "google", Label: "Google Cloud"},
		tui.Item{Label: "Cancel", Cancel: true},
	}
}

type configField struct {
	label       string
	value       string
	validate    func(string) error
	advanced    bool
	domainOnly  bool
	noDomain    bool
	acmeDNSOnly bool
}

type configForm struct {
	fields         []configField
	index          int
	input          textinput.Model
	oidcIssuerURL  string
	seedVersion    string
	err            error
	showNetworking bool
	domain         string
	dnsProvider    string
	cancel         bool
	complete       bool
}

// RunConfigWizard runs an interactive form and returns a cluster configuration.
func RunConfigWizard(oidcIssuerURL, seedVersion string) (*clusterconfig.ClusterConfig, error) {
	m := newConfigForm(oidcIssuerURL, seedVersion)
	got, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, fmt.Errorf("run cluster config form: %w", err)
	}
	form, ok := got.(configForm)
	if !ok {
		return nil, fmt.Errorf("cluster config form returned unexpected model")
	}
	if form.cancel {
		return nil, fmt.Errorf("cluster config creation cancelled")
	}
	if !form.complete {
		return nil, fmt.Errorf("cluster config creation did not complete")
	}
	return form.config()
}

// newConfigForm creates the initial model for the cluster config form.
func newConfigForm(oidcIssuerURL, seedVersion string) configForm {
	draft := clusterconfig.NewDraftConfig("aws")
	if oidcIssuerURL != "" {
		draft.Cluster.OIDC.IssuerURL = oidcIssuerURL
	}
	provider := draft.Cluster.Providers[0]
	zone := "us-east-1a"
	for name := range provider.Zones {
		zone = name
		break
	}
	pool := draft.Cluster.Pools["control-plane"]
	fields := []configField{
		{label: "Cluster name", value: draft.Cluster.Name, validate: tui.Required("cluster name")},
		{label: "Cluster ID / slug", value: draft.Cluster.ID, validate: validateClusterID},
		{label: "Cluster domain (optional)", validate: validateOptionalDomain},
		{label: "DNS provider (aws-route53 or blank for manual)", validate: validateDNSProvider, domainOnly: true},
		{label: "ACME account email (optional)", acmeDNSOnly: true},
		{label: "Kubernetes API hostname", validate: validateDomain, noDomain: true},
		{label: "AWS region", value: provider.Region, validate: tui.Required("AWS region")},
		{label: "AWS profile (optional)", value: provider.Profile},
		{label: "Initial platform components (recommended, minimal, none)", value: seeds.Recommended, validate: validateSeedName},
		{label: "Configure networking options?", value: "no", validate: validateYesNo},
		{label: "VPC IPv4 CIDR", value: provider.VPC.V4CIDR, validate: tui.Required("VPC IPv4 CIDR"), advanced: true},
		{label: "AWS availability zone", value: zone, validate: tui.Required("AWS availability zone"), advanced: true},
		{label: "Control-plane architecture", value: pool.Arch, validate: validateArch},
		{label: "Control-plane instance type", value: pool.InstanceType, validate: tui.Required("instance type")},
		{label: "Control-plane size", value: strconv.Itoa(pool.Size), validate: validatePositiveInt},
	}
	if oidcIssuerURL == "" {
		oidcField := configField{label: "OIDC issuer URL", value: draft.Cluster.OIDC.IssuerURL, validate: tui.Required("OIDC issuer URL")}
		fields = append(fields[:2], append([]configField{oidcField}, fields[2:]...)...)
	}
	input := textinput.New()
	input.Focus()
	input.CharLimit = 256
	input.SetValue(fields[0].value)
	input.CursorEnd()
	return configForm{fields: fields, input: input, oidcIssuerURL: oidcIssuerURL, seedVersion: seedVersion}
}

// Init starts cursor blinking for the cluster config form.
func (m configForm) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles text entry, cancellation, and field submission for the
// cluster config form.
func (m configForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancel = true
			return m, tea.Quit
		case "enter", "tab":
			return m.moveNext()
		case "shift+tab":
			return m.movePrevious()
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the active cluster config form field.
func (m configForm) View() string {
	if m.cancel || m.complete {
		return "\n"
	}
	title := lipgloss.NewStyle().Foreground(tui.ColorWhite).Background(tui.ColorPrimary).Padding(0, 1).Render("New cluster config")
	progress := fmt.Sprintf("%d/%d", m.index+1, len(m.fields))
	label := lipgloss.NewStyle().Bold(true).Render(m.fields[m.index].label)
	var errText string
	if m.err != nil {
		errText = "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#d20f39")).Render(m.err.Error())
	}
	help := "enter/tab: next  esc: cancel"
	if m.canMovePrevious() {
		help = "enter/tab: next  shift+tab: back  esc: cancel"
	}
	return fmt.Sprintf("\n%s %s\n\n%s\n%s%s\n\n%s\n", title, progress, label, m.input.View(), errText, help)
}

// moveNext validates and stores the active field, advancing to the next field
// or completing the form.
func (m configForm) moveNext() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	field := m.fields[m.index]
	if field.validate != nil {
		if err := field.validate(value); err != nil {
			m.err = err
			return m, nil
		}
	}
	m.fields[m.index].value = value
	if field.label == "Configure networking options?" {
		m.showNetworking = strings.EqualFold(value, "yes") || strings.EqualFold(value, "y")
	}
	if field.label == "Cluster domain (optional)" {
		m.domain = value
	}
	if field.label == "DNS provider (aws-route53 or blank for manual)" {
		m.dnsProvider = value
	}
	m.err = nil
	m.index = m.nextIndex(m.index + 1)
	if m.index >= len(m.fields) {
		m.complete = true
		return m, tea.Quit
	}
	m.input.SetValue(m.fields[m.index].value)
	m.input.CursorEnd()
	return m, nil
}

// movePrevious stores the active field and returns to the previous visible
// field without validating the active field.
func (m configForm) movePrevious() (tea.Model, tea.Cmd) {
	m.storeCurrentValue()
	m.err = nil
	m.index = m.previousIndex(m.index - 1)
	if m.index < 0 {
		m.index = 0
	}
	m.input.SetValue(m.fields[m.index].value)
	m.input.CursorEnd()
	return m, nil
}

// canMovePrevious reports whether there is a previous visible field in the
// current wizard.
func (m configForm) canMovePrevious() bool {
	return m.previousIndex(m.index-1) >= 0
}

// storeCurrentValue saves the input value for the active field so navigation
// never discards partially edited answers.
func (m *configForm) storeCurrentValue() {
	value := strings.TrimSpace(m.input.Value())
	m.fields[m.index].value = value
	if m.fields[m.index].label == "Configure networking options?" {
		m.showNetworking = strings.EqualFold(value, "yes") || strings.EqualFold(value, "y")
	}
	if m.fields[m.index].label == "Cluster domain (optional)" {
		m.domain = value
	}
	if m.fields[m.index].label == "DNS provider (aws-route53 or blank for manual)" {
		m.dnsProvider = value
	}
}

// nextIndex returns the next visible field index, skipping advanced networking
// fields unless the user requested them.
func (m configForm) nextIndex(index int) int {
	for index < len(m.fields) && !m.fieldVisible(m.fields[index]) {
		index++
	}
	return index
}

// previousIndex returns the previous visible field index, skipping advanced
// networking fields unless the user requested them.
func (m configForm) previousIndex(index int) int {
	for index >= 0 && !m.fieldVisible(m.fields[index]) {
		index--
	}
	return index
}

// fieldVisible reports whether a field applies to the current wizard answers.
func (m configForm) fieldVisible(field configField) bool {
	if field.advanced && !m.showNetworking {
		return false
	}
	if field.domainOnly && m.domain == "" {
		return false
	}
	if field.noDomain && m.domain != "" {
		return false
	}
	if field.acmeDNSOnly && !(&clusterconfig.DomainProvider{Kind: m.dnsProvider}).SupportsACME() {
		return false
	}
	return true
}

// config converts completed form answers into a cluster configuration.
func (m configForm) config() (*clusterconfig.ClusterConfig, error) {
	values := map[string]string{}
	for _, field := range m.fields {
		values[field.label] = field.value
	}
	size, err := strconv.Atoi(values["Control-plane size"])
	if err != nil {
		return nil, fmt.Errorf("parse control-plane size: %w", err)
	}
	cfg := clusterconfig.NewDraftConfig("aws")
	cfg.Cluster.Name = values["Cluster name"]
	cfg.Cluster.ID = values["Cluster ID / slug"]
	cfg.Cluster.OIDC.IssuerURL = m.oidcIssuerURL
	if cfg.Cluster.OIDC.IssuerURL == "" {
		cfg.Cluster.OIDC.IssuerURL = values["OIDC issuer URL"]
	}
	seedName, err := seeds.ParseName(values["Initial platform components (recommended, minimal, none)"])
	if err != nil {
		return nil, err
	}
	if seedName != seeds.None {
		cfg.Cluster.Seed.Name = seedName
		if m.seedVersion == "" {
			return nil, fmt.Errorf("seed version is required")
		}
		cfg.Cluster.Seed.Version = m.seedVersion
	}
	cfg.Cluster.Pools["control-plane"] = clusterconfig.Pool{
		Arch:         values["Control-plane architecture"],
		InstanceType: values["Control-plane instance type"],
		Size:         size,
	}
	provider := cfg.Cluster.Providers[0]
	provider.Region = values["AWS region"]
	provider.Profile = values["AWS profile (optional)"]
	provider.VPC.V4CIDR = values["VPC IPv4 CIDR"]
	zone := values["AWS availability zone"]
	if !m.showNetworking {
		zone = provider.Region + "a"
	}
	provider.Zones = map[string][]clusterconfig.Subnet{
		zone: provider.Zones["us-east-1a"],
	}
	domain := values["Cluster domain (optional)"]
	if domain == "" {
		cfg.Cluster.Kubernetes.APIHostname = values["Kubernetes API hostname"]
		cfg.Cluster.Registry.Hostname = cfg.Cluster.ID + "-registry.local"
		provider.LoadBalancers = nil
	} else {
		configuredDomain := clusterconfig.Domain{Zone: domain}
		if values["DNS provider (aws-route53 or blank for manual)"] == "aws-route53" {
			configuredDomain.Provider = &clusterconfig.DomainProvider{Kind: "aws-route53"}
			if email := values["ACME account email (optional)"]; email != "" {
				cfg.Cluster.ACME = &clusterconfig.ACME{Email: email}
			}
		}
		cfg.Cluster.Domains = []clusterconfig.Domain{configuredDomain}
		cfg.Cluster.Kubernetes.APIHostname = "k8s." + domain
		cfg.Cluster.Kubernetes.APILoadBalancer = "main"
		cfg.Cluster.Registry.Hostname = "registry." + domain
		provider.LoadBalancers = map[string]clusterconfig.LoadBalancer{
			"main": {
				Public:  true,
				Subnets: "public",
				Listeners: []clusterconfig.Listener{
					{Port: 443, Pool: "control-plane"},
					{Port: 6443, Pool: "control-plane"},
				},
			},
		}
	}
	cfg.Cluster.Providers[0] = provider
	return cfg, nil
}

// validateClusterID validates cluster IDs using the clusterconfig package.
func validateClusterID(value string) error {
	if err := clusterconfig.ValidateClusterID(value); err != nil {
		return fmt.Errorf("cluster ID %w", err)
	}
	return nil
}

// validateOptionalDomain accepts an empty domain or validates a DNS name.
func validateOptionalDomain(value string) error {
	if value == "" {
		return nil
	}
	return validateDomain(value)
}

// validateDomain validates a DNS name using the cluster config contract.
func validateDomain(value string) error {
	if err := clusterconfig.ValidateDomainName(value); err != nil {
		return fmt.Errorf("domain %w", err)
	}
	return nil
}

// validateDNSProvider validates the DNS providers supported by cluster create.
func validateDNSProvider(value string) error {
	if value == "" || value == "aws-route53" {
		return nil
	}
	return fmt.Errorf("DNS provider must be aws-route53 or blank for manual DNS")
}

// validateArch validates the supported control-plane CPU architectures.
func validateArch(value string) error {
	if value != "amd64" && value != "arm64" {
		return fmt.Errorf("architecture must be amd64 or arm64")
	}
	return nil
}

// validateSeedName validates the supported initial platform component seeds.
func validateSeedName(value string) error {
	if _, err := seeds.ParseName(value); err != nil {
		return err
	}
	return nil
}

// validateYesNo validates a yes/no form response.
func validateYesNo(value string) error {
	switch strings.ToLower(value) {
	case "yes", "y", "no", "n":
		return nil
	default:
		return fmt.Errorf("must be yes or no")
	}
}

// validatePositiveInt validates positive integer form values.
func validatePositiveInt(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("must be a number")
	}
	if n < 1 {
		return fmt.Errorf("must be at least 1")
	}
	return nil
}
