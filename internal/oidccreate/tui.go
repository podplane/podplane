// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidccreate

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/infrafiles"
	"github.com/podplane/podplane/internal/oidcconfig"
	"github.com/podplane/podplane/internal/tui"
)

// configField stores one OIDC wizard prompt, answer, and validation rule.
type configField struct {
	label       string
	value       string
	description string
	validate    func(string) error
}

// configForm tracks OIDC wizard answers, the active input, and completion or cancellation.
type configForm struct {
	kind      string
	provider  *oidcconfig.Provider
	connector string
	clientID  string
	fields    []configField
	index     int
	input     textinput.Model
	width     int
	err       error
	cancel    bool
	complete  bool
}

// RunConfigWizard collects Truster settings, reusing a supplied cluster
// provider or asking for cloud settings when nil. An empty clientID prompts
// for the first cluster ID. Login connector selection is always explicit.
func RunConfigWizard(ctx context.Context, provider *oidcconfig.Provider, clientID string) (*oidcconfig.Config, error) {
	providerKind := ""
	if provider != nil {
		providerKind = provider.Kind
	} else {
		kind, cancelled, err := infrafiles.SelectCloudProvider(ctx, "Which cloud provider should host the OIDC server?")
		if err != nil {
			return nil, err
		}
		if cancelled {
			return nil, fmt.Errorf("OIDC config creation cancelled")
		}
		providerKind = kind
	}
	connector, cancelled, err := tui.SelectList(ctx, "Select login connector", "Which login provider should users sign in with?", []list.Item{
		tui.Item{Key: "google", Label: "Google"},
		tui.Item{Key: "github", Label: "GitHub"},
		tui.Item{Label: "Cancel", Cancel: true},
	})
	if err != nil {
		return nil, err
	}
	if cancelled {
		return nil, fmt.Errorf("OIDC config creation cancelled")
	}
	m, err := newConfigForm(providerKind, connector, clientID, provider)
	if err != nil {
		return nil, err
	}
	got, err := tui.Run(ctx, m)
	if err != nil {
		return nil, fmt.Errorf("run OIDC config form: %w", err)
	}
	form, ok := got.(configForm)
	if !ok {
		return nil, fmt.Errorf("OIDC config form returned unexpected model")
	}
	if form.cancel {
		return nil, fmt.Errorf("OIDC config creation cancelled")
	}
	if !form.complete {
		return nil, fmt.Errorf("OIDC config creation did not complete")
	}
	return form.config()
}

// RunInline continues the current form without discarding entered answers.
func (m configForm) RunInline(ctx context.Context) (tea.Model, error) {
	for !m.complete {
		field := m.fields[m.index]
		if field.description != "" {
			fmt.Println(field.description)
		}
		value, err := tui.Input(ctx, "New Truster config", field.label, m.input.Value(), field.validate)
		if err != nil {
			return nil, err
		}
		m.input.SetValue(value)
		updated, _ := m.moveNext()
		m = updated.(configForm)
	}
	return m, nil
}

// newConfigForm creates the initial model for the Truster config form for
// providerKind and connector, asking for the first cluster ID only when the
// caller has not supplied its client ID.
func newConfigForm(providerKind, connector, clientID string, provider *oidcconfig.Provider) (configForm, error) {
	draft, err := oidcconfig.NewDraftConfig(providerKind, connector)
	if err != nil {
		return configForm{}, err
	}
	o := draft.OIDC
	if provider != nil {
		o.Provider = *provider
		prefix, suffix := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:", provider.Region, provider.Account), ""
		if providerKind == "google" {
			prefix, suffix = "projects/"+provider.Project+"/secrets/", "/versions/latest"
		}
		secrets := o.Truster["secrets"].(map[string]any)
		secrets["signing_key_name"] = prefix + "truster-signing-key" + suffix
		secrets["encryption_key_name"] = prefix + "truster-encryption-key" + suffix
		o.Truster["user_login_connectors"].(map[string]any)[connector].(map[string]any)["credentials_secret"] = prefix + "truster-" + connector + suffix
	}
	fields := []configField{
		{label: "OIDC hostname", value: o.Hostname, validate: tui.Required("OIDC hostname")},
		{label: "DNS zone", value: o.Domain.Zone, validate: tui.Required("DNS zone")},
	}
	if clientID == "" {
		fields = append(fields, configField{label: "First cluster ID", validate: clusterconfig.ValidateClusterID})
	}
	fields = append(fields, configField{
		label: "Initial administrator email",
		description: "Use the verified email you sign in with at Google or GitHub.\n" +
			"Saved in this server's policy to grant podplane:admins access\n" +
			"to the first cluster. Other users remain denied.",
		validate: validateAdminEmail,
	})
	secret := "secret ARN"
	switch providerKind {
	case "google":
		secret = "Secret Manager version"
		fields = append(fields,
			configField{label: "Cloud DNS managed zone name", value: o.Domain.Provider.HostedZoneName, validate: tui.Required("Cloud DNS managed zone name")},
		)
		if provider == nil {
			fields = append(fields,
				configField{label: "Google Cloud project ID", value: o.Provider.Project, validate: tui.Required("Google Cloud project ID")},
				configField{label: "Google Cloud region", value: o.Provider.Region, validate: tui.Required("Google Cloud region")},
				configField{label: "Google Cloud zone", value: o.Provider.Zone, validate: tui.Required("Google Cloud zone")},
			)
		}
	case "aws":
		if provider == nil {
			fields = append(fields,
				configField{label: "AWS region", value: o.Provider.Region, validate: tui.Required("AWS region")},
				configField{label: "AWS account ID", value: o.Provider.Account, validate: tui.Required("AWS account ID")},
				configField{label: "AWS profile", value: o.Provider.Profile, validate: tui.Required("AWS profile")},
			)
		}
	}
	secrets := o.Truster["secrets"].(map[string]any)
	login := o.Truster["user_login_connectors"].(map[string]any)[connector].(map[string]any)
	fields = append(fields,
		configField{label: credentialsLabel(login, secret), value: login["credentials_secret"].(string), validate: tui.Required("OAuth client credentials " + secret)},
		configField{label: "Signing key " + secret, value: secrets["signing_key_name"].(string), validate: tui.Required("signing key " + secret)},
		configField{label: "Encryption key " + secret, value: secrets["encryption_key_name"].(string), validate: tui.Required("encryption key " + secret)},
	)
	input := textinput.New()
	input.Focus()
	input.CharLimit = 512
	input.SetValue(fields[0].value)
	input.CursorEnd()
	return configForm{kind: providerKind, provider: provider, connector: connector, clientID: clientID, fields: fields, input: input, width: 80}, nil
}

// Init starts cursor blinking for the Truster config form.
func (m configForm) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles text entry, cancellation, and field submission for the Easy
// OIDC config form.
func (m configForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
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

// View renders the active Truster config form field.
func (m configForm) View() string {
	if m.cancel || m.complete {
		return "\n"
	}
	title := lipgloss.NewStyle().Foreground(tui.ColorWhite).Background(tui.ColorPrimary).Padding(0, 1).Render("New Truster config")
	progress := fmt.Sprintf("%d/%d", m.index+1, len(m.fields))
	label := lipgloss.NewStyle().Bold(true).Render(m.fields[m.index].label)
	if description := m.fields[m.index].description; description != "" {
		label += "\n" + description + "\n"
	}
	var errText string
	if m.err != nil {
		errText = "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#d20f39")).Render(m.err.Error())
	}
	help := tui.Shortcuts("`Enter/Tab` Next · `Esc` Cancel")
	if m.index > 0 {
		help = tui.Shortcuts("`Enter/Tab` Next · `Shift+Tab` Back · `Esc` Cancel")
	}
	// Keep the input unbounded so textinput never scrolls it horizontally.
	// Wrap the rendered text (including its cursor), not the stored value.
	view := fmt.Sprintf("\n%s %s\n\n%s\n%s%s\n\n%s\n", title, progress, label, m.input.View(), errText, help)
	return ansi.Hardwrap(view, max(1, m.width-1), true)
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
	m.err = nil
	m.index++
	if m.index >= len(m.fields) {
		m.complete = true
		return m, tea.Quit
	}
	m.input.SetValue(m.fields[m.index].value)
	m.input.CursorEnd()
	return m, nil
}

// movePrevious stores the active field and returns to the previous field
// without validating the active field.
func (m configForm) movePrevious() (tea.Model, tea.Cmd) {
	m.fields[m.index].value = strings.TrimSpace(m.input.Value())
	m.err = nil
	m.index--
	if m.index < 0 {
		m.index = 0
	}
	m.input.SetValue(m.fields[m.index].value)
	m.input.CursorEnd()
	return m, nil
}

// config converts completed form answers into an OIDC configuration and
// validates it.
func (m configForm) config() (*oidcconfig.Config, error) {
	values := map[string]string{}
	for _, field := range m.fields {
		values[field.label] = field.value
	}
	cfg, err := oidcconfig.NewDraftConfig(m.kind, m.connector)
	if err != nil {
		return nil, err
	}
	o := &cfg.OIDC
	o.Hostname = values["OIDC hostname"]
	o.Domain.Zone = values["DNS zone"]
	secret := "secret ARN"
	switch m.kind {
	case "google":
		secret = "Secret Manager version"
		o.Domain.Provider.HostedZoneName = values["Cloud DNS managed zone name"]
		o.Provider.Project = values["Google Cloud project ID"]
		o.Provider.Region = values["Google Cloud region"]
		o.Provider.Zone = values["Google Cloud zone"]
	case "aws":
		o.Provider.Region = values["AWS region"]
		o.Provider.Account = values["AWS account ID"]
		o.Provider.Profile = values["AWS profile"]
	}
	if m.provider != nil {
		o.Provider = *m.provider
	}
	o.Truster["secrets"] = map[string]any{
		"signing_key_name":    values["Signing key "+secret],
		"encryption_key_name": values["Encryption key "+secret],
	}
	login := o.Truster["user_login_connectors"].(map[string]any)[m.connector].(map[string]any)
	login["credentials_secret"] = values[credentialsLabel(login, secret)]
	clientID := m.clientID
	if clientID == "" {
		clientID = values["First cluster ID"]
		if err := clusterconfig.ValidateClusterID(clientID); err != nil {
			return nil, fmt.Errorf("first cluster ID: %w", err)
		}
	}
	email := strings.ToLower(strings.TrimSpace(values["Initial administrator email"]))
	if err := validateAdminEmail(email); err != nil {
		return nil, err
	}
	policy := o.Truster["static_policy"].(map[string]any)
	policy["user_group_mappings"] = map[string]any{
		"initial-admin": map[string]any{email: []any{"podplane:admins"}},
	}
	policy["clients"].(map[string]any)[clientID] = map[string]any{
		"user_group_mapping": "initial-admin",
	}
	if err := oidcconfig.Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// credentialsLabel returns the form label for a draft login connector's OAuth
// client credentials, naming the connector and the form of secret reference.
func credentialsLabel(login map[string]any, secret string) string {
	return login["display_name"].(string) + " OAuth client credentials " + secret
}

// validateAdminEmail accepts a single mailbox, not a display name or wildcard.
func validateAdminEmail(value string) error {
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || strings.ContainsAny(value, "*?") {
		return fmt.Errorf("enter your sign-in email address, without a display name")
	}
	return nil
}
