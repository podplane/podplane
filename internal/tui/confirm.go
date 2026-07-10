// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Confirm asks the user to confirm an action with the requested selected option,
// using Bubble Tea in terminals and a plain text prompt when interactive
// terminal UI is unavailable.
func Confirm(message string, autoApprove bool, selected int) (bool, error) {
	if autoApprove {
		return true, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return confirmText(message)
	}
	m, err := tea.NewProgram(confirmModel{message: message, selected: selected}).Run()
	if err != nil {
		return false, fmt.Errorf("error running confirmation prompt: %w", err)
	}
	if m, ok := m.(confirmModel); ok {
		return m.confirmed, nil
	}
	return false, fmt.Errorf("error processing confirmation")
}

// confirmText asks for an explicit "yes" response using standard input and
// output.
func confirmText(message string) (bool, error) {
	fmt.Printf("%s Type yes to continue: ", message)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

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

// wrapWords wraps text at word boundaries to fit width.
func wrapWords(text string, width int) string {
	if width <= 0 {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var b strings.Builder
	line := ""
	for _, word := range words {
		if line == "" {
			line = word
			continue
		}
		if len(line)+1+len(word) > width {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
			line = word
			continue
		}
		line += " " + word
	}
	if line != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}
