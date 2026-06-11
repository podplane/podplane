// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidccreate

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/podplane/podplane/internal/oidcconfig"
	"github.com/podplane/podplane/internal/tui"
)

type configField struct {
	label    string
	value    string
	validate func(string) error
}

type configForm struct {
	fields   []configField
	index    int
	input    textinput.Model
	err      error
	cancel   bool
	complete bool
}

// RunConfigWizard runs an interactive form for an Easy OIDC config.
func RunConfigWizard() (*oidcconfig.Config, error) {
	m := newConfigForm()
	got, err := tea.NewProgram(m).Run()
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

// newConfigForm creates the initial model for the Easy OIDC config form.
func newConfigForm() configForm {
	draft := oidcconfig.NewDraftConfig("aws")
	fields := []configField{
		{label: "OIDC hostname", value: draft.OIDC.Hostname, validate: tui.Required("OIDC hostname")},
		{label: "DNS zone", value: draft.OIDC.Domain.Zone, validate: tui.Required("DNS zone")},
		{label: "AWS region", value: draft.OIDC.Provider.Region, validate: tui.Required("AWS region")},
		{label: "AWS account ID", value: draft.OIDC.Provider.Account, validate: tui.Required("AWS account ID")},
		{label: "AWS profile", value: draft.OIDC.Provider.Profile, validate: tui.Required("AWS profile")},
		{label: "Connector kind", value: draft.OIDC.Connector.Kind, validate: validateConnector},
		{label: "Connector client secret ARN", value: draft.OIDC.Connector.ClientSecretARN, validate: tui.Required("connector client secret ARN")},
		{label: "Signing key secret ARN", value: draft.OIDC.SigningKeySecretARN, validate: tui.Required("signing key secret ARN")},
	}
	input := textinput.New()
	input.Focus()
	input.CharLimit = 512
	input.SetValue(fields[0].value)
	input.CursorEnd()
	return configForm{fields: fields, input: input}
}

// Init starts cursor blinking for the Easy OIDC config form.
func (m configForm) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles text entry, cancellation, and field submission for the Easy
// OIDC config form.
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

// View renders the active Easy OIDC config form field.
func (m configForm) View() string {
	if m.cancel || m.complete {
		return "\n"
	}
	title := lipgloss.NewStyle().Foreground(tui.ColorWhite).Background(tui.ColorPrimary).Padding(0, 1).Render("New Easy OIDC config")
	progress := fmt.Sprintf("%d/%d", m.index+1, len(m.fields))
	label := lipgloss.NewStyle().Bold(true).Render(m.fields[m.index].label)
	var errText string
	if m.err != nil {
		errText = "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#d20f39")).Render(m.err.Error())
	}
	help := "enter/tab: next  esc: cancel"
	if m.index > 0 {
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

// config converts completed form answers into an OIDC configuration.
func (m configForm) config() (*oidcconfig.Config, error) {
	values := map[string]string{}
	for _, field := range m.fields {
		values[field.label] = field.value
	}
	cfg := oidcconfig.NewDraftConfig("aws")
	cfg.OIDC.Hostname = values["OIDC hostname"]
	cfg.OIDC.Domain.Zone = values["DNS zone"]
	cfg.OIDC.Provider.Region = values["AWS region"]
	cfg.OIDC.Provider.Account = values["AWS account ID"]
	cfg.OIDC.Provider.Profile = values["AWS profile"]
	cfg.OIDC.Connector.Kind = values["Connector kind"]
	cfg.OIDC.Connector.ClientSecretARN = values["Connector client secret ARN"]
	cfg.OIDC.SigningKeySecretARN = values["Signing key secret ARN"]
	return cfg, nil
}

// validateConnector validates supported Easy OIDC connector kinds.
func validateConnector(value string) error {
	switch value {
	case "google", "github":
		return nil
	default:
		return fmt.Errorf("connector kind must be google or github")
	}
}
