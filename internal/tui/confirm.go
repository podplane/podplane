// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Confirm asks the user to confirm an action with the requested selected option,
// using Bubble Tea in terminals and an inline prompt when interactive
// terminal UI is unavailable.
func Confirm(ctx context.Context, message string, autoApprove bool, selected int) (bool, error) {
	if autoApprove {
		return true, nil
	}
	m, err := Run(ctx, confirmModel{message: message, selected: selected})
	if err != nil {
		return false, fmt.Errorf("error running confirmation prompt: %w", err)
	}
	if m, ok := m.(confirmModel); ok {
		return m.confirmed, nil
	}
	return false, fmt.Errorf("error processing confirmation")
}

// RunInline requires an explicit yes, even when Yes is highlighted in the TUI.
func (m confirmModel) RunInline(ctx context.Context) (tea.Model, error) {
	fmt.Printf("%s Type yes to continue: ", ansi.Strip(m.message))
	line, err := ReadLine(ctx)
	m.confirmed, m.done = strings.EqualFold(strings.TrimSpace(line), "yes"), true
	return m, err
}

// confirmModel tracks the yes/no selection, result, and available prompt width.
type confirmModel struct {
	message   string
	selected  int
	confirmed bool
	done      bool
	width     int
}

// Init starts the confirmation prompt without any startup command.
func (m confirmModel) Init() tea.Cmd {
	return nil
}

// Update handles key presses for the confirmation prompt.
func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.done = true
			return m, tea.Quit
		case "left", "h", "right", "l", "tab":
			if m.selected == 0 {
				m.selected = 1
			} else {
				m.selected = 0
			}
			return m, nil
		case "y":
			m.confirmed = true
			m.done = true
			return m, tea.Quit
		case "n":
			m.confirmed = false
			m.done = true
			return m, tea.Quit
		case "enter":
			m.confirmed = m.selected == 0
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View renders the confirmation prompt.
func (m confirmModel) View() string {
	if m.done {
		return "\n"
	}
	titleStyle := lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorWhite).Padding(0, 1)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorSecondary)
	title, body := splitConfirmMessage(m.message)
	message := body
	if m.width > 4 {
		message = wrapWords(message, m.width-4)
	}
	yes := "Yes"
	no := "No"
	if m.selected == 0 {
		yes = selectedStyle.Render("> " + yes)
		no = "  " + no
	} else {
		yes = "  " + yes
		no = selectedStyle.Render("> " + no)
	}
	if message != "" {
		message += "\n\n"
	}
	return fmt.Sprintf("\n%s\n\n%s%s    %s\n\n", titleStyle.Render(title), message, yes, no)
}

// splitConfirmMessage splits a confirmation message into title and body.
func splitConfirmMessage(message string) (string, string) {
	title, body, ok := strings.Cut(message, "\n")
	if !ok {
		return message, ""
	}
	return title, strings.TrimSpace(body)
}

// wrapWords wraps styled text while preserving explicit line breaks.
func wrapWords(text string, width int) string {
	if width <= 0 {
		return text
	}
	return ansi.Wrap(text, width, "")
}
