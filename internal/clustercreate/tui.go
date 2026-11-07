// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/podplane/podplane/internal/cloud"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/pkg/seeds"
)

// configField describes one field in the cluster configuration wizard.
type configField struct {
	label    string
	value    string
	choices  []string
	validate func(string) error
	visible  func(configForm) bool
}

// configForm holds the cluster configuration wizard state.
type configForm struct {
	fields         []configField
	providerKind   string
	index          int
	input          textinput.Model
	seedVersion    string
	err            error
	showNetworking bool
	domain         string
	dnsProvider    string
	cancel         bool
	complete       bool
	ctx            context.Context
	verifyDNS      func(context.Context, string, string, string, string) error
	verification   *tui.Verification
	width          int
}

// RunConfigWizard runs the AWS or Google Cloud form and returns a cluster
// configuration without an OIDC issuer URL, which the caller sets once it has
// chosen or created an issuer for the cluster.
func RunConfigWizard(ctx context.Context, providerKind, seedVersion string) (*clusterconfig.ClusterConfig, error) {
	m, err := newConfigForm(providerKind, seedVersion)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.ctx, m.verifyDNS = ctx, cloud.CheckRoute53Zone
	got, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
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
func newConfigForm(providerKind, seedVersion string) (configForm, error) {
	draft, err := clusterconfig.NewDraftConfig(providerKind)
	if err != nil {
		return configForm{}, err
	}
	provider := draft.Cluster.Providers[0]
	zone := ""
	for name := range provider.Zones {
		zone = name
		break
	}
	pool := draft.Cluster.Pools["control-plane"]
	regionField := configField{label: "AWS region", value: provider.Region, validate: clusterconfig.ValidateAWSRegion}
	credentialsField := configField{label: "AWS profile (optional)", value: provider.Profile}
	zoneField := configField{label: "AWS availability zone", value: zone, validate: tui.Required("AWS availability zone")}
	dnsField := configField{label: "DNS provider", value: "manual", choices: []string{"manual", "aws-route53"}}
	if providerKind == "google" {
		regionField = configField{label: "Google Cloud region", value: provider.Region, validate: validateGoogleRegion}
		credentialsField = configField{label: "Google Cloud project ID", value: provider.Project, validate: tui.Required("Google Cloud project ID")}
		zoneField = configField{label: "Google Cloud zone", value: zone, validate: tui.Required("Google Cloud zone")}
		dnsField.choices = []string{"manual", "google-cloud-dns"}
	}
	zoneField.visible = func(m configForm) bool { return m.showNetworking }
	dnsField.visible = func(m configForm) bool { return m.domain != "" }
	fields := []configField{
		{label: "Cluster name", value: draft.Cluster.Name, validate: tui.Required("cluster name")},
		{label: "Cluster ID / slug", value: draft.Cluster.ID, validate: validateClusterID},
		regionField,
		credentialsField,
		{label: "Cluster domain (optional)", validate: validateOptionalDomain},
		dnsField,
		{label: "Google Cloud DNS managed-zone resource name", validate: tui.Required("managed-zone resource name"), visible: func(m configForm) bool {
			return m.domain != "" && m.providerKind == "google" && m.dnsProvider == "google-cloud-dns"
		}},
		{label: "ACME account email (optional)", visible: func(m configForm) bool {
			return (&clusterconfig.DomainProvider{Kind: m.dnsProvider}).SupportsACME()
		}},
		{label: "Kubernetes API hostname", validate: validateDomain, visible: func(m configForm) bool { return m.domain == "" }},
		{label: "Cluster seed", value: seeds.Agentic, choices: []string{seeds.Agentic, seeds.Platform, seeds.Minimal, seeds.None}},
		{label: "Configure networking options?", value: "no", choices: []string{"yes", "no"}},
		{label: "VPC IPv4 CIDR", value: provider.VPC.V4CIDR, validate: validateVPCCIDR, visible: func(m configForm) bool { return m.showNetworking }},
		zoneField,
		{label: "Control plane architecture", value: pool.Arch, choices: []string{"arm64", "amd64"}},
		{label: "Control plane instance type", value: pool.InstanceType, validate: tui.Required("instance type")},
		{label: "Control plane size", value: strconv.Itoa(pool.Size), validate: validatePositiveInt},
	}
	input := textinput.New()
	input.Focus()
	input.CharLimit = 256
	input.SetValue(fields[0].value)
	input.CursorEnd()
	return configForm{fields: fields, input: input, providerKind: providerKind, seedVersion: seedVersion}, nil
}

// Init starts cursor blinking for the cluster config form.
func (m configForm) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles text entry, cancellation, and field submission for the
// cluster config form.
func (m configForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
	}
	if m.verification != nil {
		cmd := m.verification.Update(msg)
		if !m.verification.Done {
			return m, cmd
		}
		m.err = m.verification.Err
		m.verification = nil
		if errors.Is(m.err, context.Canceled) {
			m.cancel = true
			return m, tea.Quit
		}
		if m.err != nil {
			return m, nil
		}
		return m.advance()
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel = true
			return m, tea.Quit
		case "esc":
			return m, nil
		case "enter", "tab":
			return m.moveNext()
		case "shift+tab":
			return m.movePrevious()
		}
	}
	if choices := m.fields[m.index].choices; len(choices) > 0 {
		if key, ok := msg.(tea.KeyMsg); ok {
			selected := slices.Index(choices, m.input.Value())
			switch key.String() {
			case "up", "left", "k", "h":
				selected = (selected + len(choices) - 1) % len(choices)
			case "down", "right", "j", "l":
				selected = (selected + 1) % len(choices)
			}
			m.input.SetValue(choices[selected])
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the active cluster config form field.
func (m configForm) View() string {
	if m.verification != nil {
		return m.verification.View()
	}
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
	help := "enter/tab: next  ctrl+c: cancel"
	if m.canMovePrevious() {
		help = "enter/tab: next  shift+tab: back  ctrl+c: cancel"
	}
	input := m.input.View()
	if choices := m.fields[m.index].choices; len(choices) > 0 {
		rows := make([]string, 0, len(choices))
		for _, choice := range choices {
			row := "  " + choice
			if choice == m.input.Value() {
				row = lipgloss.NewStyle().Foreground(tui.ColorSecondary).Render("> " + choice)
			}
			rows = append(rows, row)
		}
		input = strings.Join(rows, "\n")
		help = "↑/↓: select  " + help
	}
	width := m.width
	if width == 0 {
		width = 80
	}
	return ansi.Wrap(fmt.Sprintf("\n%s %s\n\n%s\n%s%s\n\n%s\n", title, progress, label, input, errText, help), max(1, width-1), "")
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
	if field.label == "AWS availability zone" {
		region := m.fields[indexForLabel(m.fields, "AWS region")].value
		if err := clusterconfig.ValidateAWSAvailabilityZone(region, value); err != nil {
			m.err = err
			return m, nil
		}
	}
	if field.label == "Google Cloud zone" {
		region := m.fields[indexForLabel(m.fields, "Google Cloud region")].value
		if err := validateGoogleZone(region, value); err != nil {
			m.err = err
			return m, nil
		}
	}
	if field.label == "Cluster name" {
		m.suggestClusterID(m.fields[m.index].value, value)
	}
	if field.label == "DNS provider" && value == "aws-route53" && m.verifyDNS != nil {
		region := m.fields[indexForLabel(m.fields, "AWS region")].value
		profile := m.fields[indexForLabel(m.fields, "AWS profile (optional)")].value
		check := tui.NewVerification(m.ctx, "Checking Route53 hosted zone", "Public hosted zone: "+m.domain, func(ctx context.Context) error {
			return m.verifyDNS(ctx, region, profile, m.domain, "")
		})
		m.verification = &check
		if m.width > 0 {
			m.verification.Update(tea.WindowSizeMsg{Width: m.width})
		}
		return m, m.verification.Start()
	}
	return m.advance()
}

// advance stores an accepted or explicitly unverified answer.
func (m configForm) advance() (tea.Model, tea.Cmd) {
	m.storeCurrentValue()
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

// suggestClusterID updates the cluster ID field to a slug of the new cluster
// name, unless the user changed it from the slug of the old name. The draft's
// name and ID follow the same rule.
func (m *configForm) suggestClusterID(oldName, newName string) {
	id := &m.fields[indexForLabel(m.fields, "Cluster ID / slug")].value
	if *id == clusterIDFromName(oldName) {
		*id = clusterIDFromName(newName)
	}
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
	if m.fields[m.index].label == "Control plane architecture" {
		instance := &m.fields[indexForLabel(m.fields, "Control plane instance type")].value
		if *instance == controlPlaneInstanceType(m.providerKind, m.fields[m.index].value) {
			*instance = controlPlaneInstanceType(m.providerKind, value)
		}
	}
	m.fields[m.index].value = value
	if m.fields[m.index].label == "Configure networking options?" {
		m.showNetworking = value == "yes"
	}
	if m.fields[m.index].label == "Cluster domain (optional)" {
		m.domain = value
	}
	if m.fields[m.index].label == "DNS provider" {
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
	return field.visible == nil || field.visible(m)
}

// config converts completed form answers into a cluster configuration.
func (m configForm) config() (*clusterconfig.ClusterConfig, error) {
	values := map[string]string{}
	for _, field := range m.fields {
		values[field.label] = field.value
	}
	size, err := strconv.Atoi(values["Control plane size"])
	if err != nil {
		return nil, fmt.Errorf("parse control-plane size: %w", err)
	}
	cfg, err := clusterconfig.NewDraftConfig(m.providerKind)
	if err != nil {
		return nil, err
	}
	cfg.Cluster.Name = values["Cluster name"]
	cfg.Cluster.ID = values["Cluster ID / slug"]
	cfg.Cluster.OIDC.IssuerURL = ""
	seedName, err := seeds.ParseName(values["Cluster seed"])
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
		Arch:         values["Control plane architecture"],
		InstanceType: values["Control plane instance type"],
		Size:         size,
	}
	provider := cfg.Cluster.Providers[0]
	provider.Region = values["AWS region"]
	provider.Profile = values["AWS profile (optional)"]
	if m.providerKind == "google" {
		provider.Region = values["Google Cloud region"]
		provider.Project = values["Google Cloud project ID"]
		if err := tui.Required("Google Cloud project ID")(provider.Project); err != nil {
			return nil, err
		}
		secretProvider := cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider]
		secretProvider.ProjectID = provider.Project
		cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider] = secretProvider
	}
	provider.VPC.V4CIDR = values["VPC IPv4 CIDR"]
	zone := values["AWS availability zone"]
	if m.providerKind == "google" {
		zone = values["Google Cloud zone"]
		if !m.showNetworking {
			zone = provider.Region + "-a"
		}
		if err := validateGoogleZone(provider.Region, zone); err != nil {
			return nil, fmt.Errorf("invalid Google Cloud zone %q: %w", zone, err)
		}
	} else {
		if !m.showNetworking {
			zone = provider.Region + "a"
		}
		if err := clusterconfig.ValidateAWSAvailabilityZone(provider.Region, zone); err != nil {
			return nil, fmt.Errorf("AWS availability zone %q %w", zone, err)
		}
	}
	if err := validateVPCCIDR(provider.VPC.V4CIDR); err != nil {
		return nil, err
	}
	vpcPrefix, err := netip.ParsePrefix(provider.VPC.V4CIDR)
	if err != nil {
		return nil, fmt.Errorf("parse VPC IPv4 CIDR: %w", err)
	}
	vpcAddr := vpcPrefix.Addr().As4()
	var subnets []clusterconfig.Subnet
	for _, defaults := range provider.Zones {
		subnets = append([]clusterconfig.Subnet(nil), defaults...)
		break
	}
	for i := range subnets {
		subnets[i].Services = append([]string(nil), subnets[i].Services...)
		subnetPrefix, err := netip.ParsePrefix(subnets[i].V4CIDR)
		if err != nil {
			return nil, fmt.Errorf("parse default subnet IPv4 CIDR: %w", err)
		}
		subnetAddr := subnetPrefix.Addr().As4()
		subnetAddr[0], subnetAddr[1] = vpcAddr[0], vpcAddr[1]
		subnets[i].V4CIDR = netip.PrefixFrom(netip.AddrFrom4(subnetAddr), subnetPrefix.Bits()).String()
	}
	provider.Zones = map[string][]clusterconfig.Subnet{
		zone: subnets,
	}
	domain := values["Cluster domain (optional)"]
	if domain == "" {
		cfg.Cluster.Kubernetes.APIHostname = values["Kubernetes API hostname"]
		cfg.Cluster.Registry.Hostname = cfg.Cluster.ID + "-registry.local"
		provider.LoadBalancers = nil
	} else {
		configuredDomain := clusterconfig.Domain{Zone: domain}
		if values["DNS provider"] == "aws-route53" {
			configuredDomain.Provider = &clusterconfig.DomainProvider{Kind: "aws-route53"}
			if email := values["ACME account email (optional)"]; email != "" {
				cfg.Cluster.ACME = &clusterconfig.ACME{Email: email}
			}
		}
		if m.providerKind == "google" && values["DNS provider"] == "google-cloud-dns" {
			zoneName := values["Google Cloud DNS managed-zone resource name"]
			if err := tui.Required("managed-zone resource name")(zoneName); err != nil {
				return nil, err
			}
			configuredDomain.Provider = &clusterconfig.DomainProvider{
				Kind: "google-cloud-dns", Project: provider.Project, HostedZoneName: zoneName,
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
	cfg.Cluster.SPIFFE.TrustDomain = cfg.Cluster.Kubernetes.APIHostname
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

// validateGoogleRegion validates Google Cloud region syntax without a cloud lookup.
func validateGoogleRegion(value string) error {
	if matched, _ := regexp.MatchString(`^[a-z]+(-[a-z]+)+[0-9]+$`, value); !matched {
		return fmt.Errorf("must be a valid Google Cloud region, such as us-central1")
	}
	return nil
}

// validateGoogleZone verifies that a Google Cloud zone belongs to the region.
func validateGoogleZone(region, zone string) error {
	if err := validateGoogleRegion(region); err != nil {
		return err
	}
	suffix := strings.TrimPrefix(zone, region+"-")
	if !strings.HasPrefix(zone, region+"-") || len(suffix) != 1 || suffix[0] < 'a' || suffix[0] > 'z' {
		return fmt.Errorf("must be a zone in region %s", region)
	}
	return nil
}

// validateVPCCIDR validates the /16 network shape used by the cluster wizard's
// automatically allocated default subnets.
func validateVPCCIDR(value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 16 || prefix != prefix.Masked() {
		return fmt.Errorf("VPC IPv4 CIDR must be a canonical IPv4 /16")
	}
	return nil
}

// indexForLabel returns the index of the field with label, or -1 if absent.
func indexForLabel(fields []configField, label string) int {
	for i := range fields {
		if fields[i].label == label {
			return i
		}
	}
	return -1
}

// controlPlaneInstanceType suggests a machine type matching the selected
// architecture. Explicitly edited instance types are kept when navigating.
func controlPlaneInstanceType(provider, arch string) string {
	if provider == "google" {
		if arch == "arm64" {
			return "t2a-standard-2"
		}
		return "e2-medium"
	}
	if arch == "arm64" {
		return "t4g.medium"
	}
	return "t3.medium"
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

// clusterIDFromName derives a cluster ID from a cluster name: lowercase
// letters and digits, with each run of other characters replaced by one
// hyphen, trimmed to the 32-character cluster ID limit.
func clusterIDFromName(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			hyphen = false
		} else {
			hyphen = true
		}
	}
	id := b.String()
	if len(id) > 32 {
		id = strings.TrimRight(id[:32], "-")
	}
	return id
}
