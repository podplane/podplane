// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InputValidator validates text entered into an input prompt.
type InputValidator func(string) error

// inputModel tracks a validated text prompt and its submission or cancellation state.
type inputModel struct {
	title    string
	label    string
	input    textinput.Model
	validate InputValidator
	err      error
	cancel   bool
	done     bool
}

// Input asks the user for one text value and validates it before returning.
func Input(title, label, value string, validate InputValidator) (string, error) {
	input := textinput.New()
	input.Focus()
	input.CharLimit = 256
	input.SetValue(value)
	input.CursorEnd()
	got, err := tea.NewProgram(inputModel{
		title:    title,
		label:    label,
		input:    input,
		validate: validate,
	}).Run()
	if err != nil {
		return "", fmt.Errorf("run input prompt: %w", err)
	}
	m, ok := got.(inputModel)
	if !ok {
		return "", fmt.Errorf("input prompt returned unexpected model")
	}
	if m.cancel {
		return "", fmt.Errorf("input cancelled")
	}
	if !m.done {
		return "", fmt.Errorf("input did not complete")
	}
	return strings.TrimSpace(m.input.Value()), nil
}

// Init starts cursor blinking for the input prompt.
func (m inputModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles text entry, cancellation, and submission for the input prompt.
func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancel = true
			return m, tea.Quit
		case "enter":
			value := strings.TrimSpace(m.input.Value())
			if m.validate != nil {
				if err := m.validate(value); err != nil {
					m.err = err
					return m, nil
				}
			}
			m.done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the input prompt.
func (m inputModel) View() string {
	if m.cancel || m.done {
		return "\n"
	}
	title := lipgloss.NewStyle().Foreground(ColorWhite).Background(ColorPrimary).Padding(0, 1).Render(m.title)
	label := lipgloss.NewStyle().Bold(true).Render(m.label)
	var errText string
	if m.err != nil {
		errText = "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#d20f39")).Render(m.err.Error())
	}
	return fmt.Sprintf("\n%s\n\n%s\n%s%s\n\nenter: continue  esc: cancel\n", title, label, m.input.View(), errText)
}

// Required returns a validator that rejects empty input.
func Required(name string) InputValidator {
	return func(value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
}
