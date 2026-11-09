// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package infrafiles

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/podplane/podplane/internal/cloud"
	"github.com/podplane/podplane/internal/tui"
)

// awsAccountLookup keeps the account lookup visible and allows manual entry.
type awsAccountLookup struct {
	lookup  func() (string, error)
	cancel  context.CancelFunc
	profile string
	spinner spinner.Model
	input   textinput.Model
	notice  string
	width   int
	account string
	err     error
	manual  bool
	aborted bool
	done    bool
}

// awsAccountResult carries the asynchronous lookup result to the UI.
type awsAccountResult struct {
	account string
	err     error
}

// RunInline resolves the account, then allows explicit confirmation or correction.
func (m awsAccountLookup) RunInline(ctx context.Context) (tea.Model, error) {
	if !m.manual {
		account, err := m.lookup()
		if err != nil {
			fmt.Println("Could not resolve AWS account; enter it manually.")
		}
		updated, _ := m.enterAccount(account, "")
		m = updated.(awsAccountLookup)
	}
	account, err := tui.Input(ctx, "Confirm AWS account", "AWS account ID", m.input.Value(), cloud.ValidateAWSAccount)
	m.account, m.done = account, true
	return m, err
}

// Init starts lookup and spinner updates without blocking keyboard input.
func (m awsAccountLookup) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		account, err := m.lookup()
		return awsAccountResult{account: account, err: err}
	})
}

// Update handles lookup completion, manual entry, and cancellation.
func (m awsAccountLookup) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if !m.manual {
				m.cancel()
				return m.enterAccount("", "Lookup stopped. Enter your AWS account ID manually.")
			}
			return m, nil
		case "ctrl+c":
			m.cancel()
			m.aborted, m.done = true, true
			return m, tea.Quit
		case "enter":
			if m.manual {
				account := strings.TrimSpace(m.input.Value())
				if err := cloud.ValidateAWSAccount(account); err != nil {
					m.err = err
					return m, nil
				}
				m.account, m.done = account, true
				return m, tea.Quit
			}
		}
	case awsAccountResult:
		if m.manual {
			return m, nil
		}
		if msg.err != nil {
			return m.enterAccount("", "Could not resolve the account. Enter your AWS account ID manually.")
		}
		return m.enterAccount(msg.account, "Resolved from the selected AWS credentials.")
	}
	var cmd tea.Cmd
	if m.manual {
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// enterAccount switches to editable confirmation without replacing the program,
// so queued Escape keys cannot accidentally cancel the next prompt.
func (m awsAccountLookup) enterAccount(account, notice string) (tea.Model, tea.Cmd) {
	m.manual, m.notice = true, notice
	m.input = textinput.New()
	m.input.CharLimit = 256
	m.input.SetValue(account)
	m.input.CursorEnd()
	cmd := m.input.Focus()
	return m, cmd
}

// View renders the pending lookup with a manual-entry escape route.
func (m awsAccountLookup) View() string {
	if m.done {
		return "\n"
	}
	if m.manual {
		title := lipgloss.NewStyle().Foreground(tui.ColorWhite).Background(tui.ColorPrimary).Padding(0, 1).Render("Confirm AWS account")
		label := lipgloss.NewStyle().Bold(true).Render("AWS account ID for infrastructure and remote state")
		errText := ""
		if m.err != nil {
			errText = "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#d20f39")).Render(m.err.Error())
		}
		return ansi.Hardwrap(fmt.Sprintf("\n%s\n\n%s\n\n%s\n%s%s\n\n%s\n", title, m.notice, label, m.input.View(), errText, tui.Shortcuts("`Enter` Continue · `Ctrl-C` Cancel")), max(1, m.width-1), true)
	}
	title := lipgloss.NewStyle().Foreground(tui.ColorWhite).Background(tui.ColorPrimary).Padding(0, 1).Render("Resolving AWS account")
	description := "Using supplied profile to resolve AWS account ID automatically"
	if m.profile == "" {
		description = "Using default AWS credentials to resolve AWS account ID automatically"
	}
	description = lipgloss.NewStyle().Width(m.width).Render(description)
	return fmt.Sprintf("\n%s\n\n%s\n\n%s Looking up account ID…\n\n%s\n", title, description, m.spinner.View(), tui.Shortcuts("`Esc` Enter manually · `Ctrl-C` Cancel"))
}
