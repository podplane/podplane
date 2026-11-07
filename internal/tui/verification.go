// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Verification is a bounded, optional online check. Escape skips verification;
// Ctrl-C cancels creation. Errors allow retry, skipping, or returning to edit.
type Verification struct {
	Done    bool
	Err     error
	Skipped bool
	title   string
	detail  string
	ctx     context.Context
	attempt context.Context
	check   func(context.Context) error
	cancel  context.CancelFunc
	spinner spinner.Model
	width   int
	failure error
}

type verificationResult struct {
	attempt context.Context
	err     error
}

// NewVerification prepares a check for embedding in a wizard.
func NewVerification(ctx context.Context, title, detail string, check func(context.Context) error) Verification {
	return Verification{ctx: ctx, title: title, detail: detail, check: check, spinner: spinner.New(), width: 80}
}

// Start begins a new attempt with its own timeout.
func (m *Verification) Start() tea.Cmd {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	m.attempt = ctx
	m.cancel = cancel
	m.failure = nil
	check := m.check
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		defer cancel()
		return verificationResult{ctx, check(ctx)}
	})
}

// Cancel stops an in-flight request when the containing program exits.
func (m Verification) Cancel() {
	if m.cancel != nil {
		m.cancel()
	}
}

// Update handles input and results without quitting the containing wizard.
func (m *Verification) Update(msg tea.Msg) tea.Cmd {
	if m.Done {
		return nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.Cancel()
			m.Done, m.Skipped = true, true
			return nil
		case "ctrl+c":
			m.Cancel()
			m.Done, m.Err = true, context.Canceled
			return nil
		case "r":
			if m.failure != nil {
				return m.Start()
			}
		case "enter":
			if m.failure != nil {
				m.Done, m.Err = true, m.failure
				return nil
			}
		}
	case verificationResult:
		if msg.attempt != m.attempt {
			return nil
		}
		m.failure = msg.err
		m.Done = msg.err == nil
		return nil
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// View renders progress or a failure with explicit skip and cancel controls.
func (m Verification) View() string {
	if m.Done {
		return "\n"
	}
	title := lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorWhite).Padding(0, 1).Render(m.title)
	status := m.spinner.View() + " Verifying…"
	help := "esc: skip verification  ctrl+c: cancel"
	if m.failure != nil {
		lines := strings.Split(ansi.Wrap(m.failure.Error(), max(1, m.width-1), ""), "\n")
		if len(lines) > 8 {
			lines = append(lines[:8], "…")
		}
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#d20f39")).Render(strings.Join(lines, "\n"))
		help = "r: retry  enter: return error  esc: skip verification  ctrl+c: cancel"
	}
	return ansi.Wrap(fmt.Sprintf("\n%s\n\n%s\n\n%s\n\n%s\n", title, m.detail, status, help), max(1, m.width-1), "")
}

// RunVerification runs an optional interactive preflight. Unattended callers
// run the same bounded check without offering a skip prompt.
func RunVerification(ctx context.Context, title, detail string, check func(context.Context) error, unattended bool) error {
	if unattended || !CanUseTUI(0, 0).OK {
		fmt.Println(title)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return check(ctx)
	}
	m := verificationProgram{NewVerification(ctx, title, detail, check)}
	defer func() { m.Cancel() }()
	result, err := tea.NewProgram(&m, tea.WithContext(ctx)).Run()
	if err != nil {
		return err
	}
	return result.(*verificationProgram).Err
}

type verificationProgram struct{ Verification }

func (m *verificationProgram) Init() tea.Cmd { return m.Start() }

func (m *verificationProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.Verification.Update(msg)
	if m.Done {
		return m, tea.Quit
	}
	return m, cmd
}
