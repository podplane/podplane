// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// ChecklistItem is a choice with an initial selection and optional prerequisites.
// Requires contains indices of earlier items that must all be checked. Items
// without prerequisites are independent; invalid initial selections are cleared.
type ChecklistItem struct {
	Label    string
	Checked  bool
	Requires []int
}

// ChecklistOptions supplies choices and caller-specific explanatory text.
type ChecklistOptions struct {
	Title, Description string
	Items              []ChecklistItem
	// ConfirmLabel defaults to "Continue"; ConfirmPrompt defaults to "Confirm selection?".
	ConfirmLabel, ConfirmPrompt string
	// AutoApprove accepts valid initial selections without prompting.
	AutoApprove bool
}

// Checklist returns selections in item order after explicit confirmation.
// Unchecking a prerequisite clears its dependents, but rechecking it does not
// select them automatically. Dependencies apply to terminal and plain-text input.
func Checklist(opts ChecklistOptions) ([]bool, error) {
	m, err := newChecklistModel(opts)
	if err != nil {
		return nil, err
	}
	if opts.AutoApprove {
		for i, item := range opts.Items {
			mark := " "
			if m.checked[i] {
				mark = "x"
			}
			fmt.Printf("[%s] %s\n", mark, item.Label)
		}
		return m.checked, nil
	}
	if !CanUseTUI(MinTUIWidth, MinTUIHeight).OK || !term.IsTerminal(int(os.Stdin.Fd())) {
		return m.readText(os.Stdin, os.Stdout)
	}
	result, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	final := result.(checklistModel)
	if !final.approved {
		return nil, fmt.Errorf("selection cancelled")
	}
	return final.checked, nil
}

// checklistModel tracks selections, prerequisites, and explicit approval focus.
type checklistModel struct {
	opts                           ChecklistOptions
	checked                        []bool
	selected, focus, width, height int
	approved                       bool
}

// newChecklistModel validates dependencies and initializes independent selection state.
func newChecklistModel(opts ChecklistOptions) (checklistModel, error) {
	if len(opts.Items) == 0 {
		return checklistModel{}, fmt.Errorf("checklist requires at least one item")
	}
	if opts.ConfirmLabel == "" {
		opts.ConfirmLabel = "Continue"
	}
	if opts.ConfirmPrompt == "" {
		opts.ConfirmPrompt = "Confirm selection?"
	}
	m := checklistModel{opts: opts, checked: make([]bool, len(opts.Items)), width: MinTUIWidth, height: MinTUIHeight}
	for i, item := range opts.Items {
		for _, prerequisite := range item.Requires {
			if prerequisite < 0 || prerequisite >= i {
				return checklistModel{}, fmt.Errorf("checklist item %d: prerequisite %d must reference an earlier item", i, prerequisite)
			}
		}
		m.checked[i] = item.Checked && m.available(i)
	}
	return m, nil
}

// available reports whether all prerequisites of an item are selected.
func (m checklistModel) available(i int) bool {
	for _, prerequisite := range m.opts.Items[i].Requires {
		if !m.checked[prerequisite] {
			return false
		}
	}
	return true
}

// setChecked updates a choice and clears any dependents whose prerequisites were lost.
func (m checklistModel) setChecked(i int, checked bool) {
	m.checked[i] = checked && m.available(i)
	for j := i + 1; j < len(m.checked); j++ {
		if !m.available(j) {
			m.checked[j] = false
		}
	}
}

// readText collects choices and enforces prerequisites without terminal control sequences.
func (m checklistModel) readText(in io.Reader, out io.Writer) ([]bool, error) {
	reader := bufio.NewReader(in)
	for i, item := range m.opts.Items {
		if !m.available(i) {
			if _, err := fmt.Fprintln(out, "[ ] "+item.Label); err != nil {
				return nil, err
			}
			continue
		}
		choices := "y/N"
		if m.checked[i] {
			choices = "Y/n"
		}
		if _, err := fmt.Fprintf(out, "%s? [%s]: ", item.Label, choices); err != nil {
			return nil, err
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
		case "y", "yes":
			m.setChecked(i, true)
		case "n", "no":
			m.setChecked(i, false)
		default:
			return nil, fmt.Errorf("expected yes or no; selection cancelled")
		}
	}
	if _, err := fmt.Fprint(out, m.opts.ConfirmPrompt+" Type yes to continue: "); err != nil {
		return nil, err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(line) != "yes" {
		return nil, fmt.Errorf("selection cancelled")
	}
	return m.checked, nil
}

// Init starts the checklist without background work.
func (m checklistModel) Init() tea.Cmd { return nil }

// Update handles selection, prerequisite enforcement, approval, and cancellation.
func (m checklistModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "tab":
			m.focus = (m.focus + 1) % 3
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
		case "up", "k":
			m.selected = max(0, m.selected-1)
			m.focus = 0
		case "down", "j":
			m.selected = min(len(m.opts.Items)-1, m.selected+1)
			m.focus = 0
		case " ":
			if m.focus == 0 && !m.small() {
				m.setChecked(m.selected, !m.checked[m.selected])
			}
		case "enter":
			if m.focus == 1 {
				return m, tea.Quit
			}
			if m.focus == 2 && !m.small() {
				m.approved = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// small reports whether the checklist's actions would be hidden by the terminal.
func (m checklistModel) small() bool {
	return m.width < MinTUIWidth || m.height < MinTUIHeight
}

// View renders choices and confirmation controls, or a resize warning.
func (m checklistModel) View() string {
	if m.small() {
		return "Resize to at least 80×24 to review selection. Esc cancels.\n"
	}
	width := min(m.width-6, 110)
	primary := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)
	var lines []string
	for i, item := range m.opts.Items {
		mark := "[ ] "
		if m.checked[i] {
			mark = "[x] "
		}
		prefix := "  "
		if m.focus == 0 && m.selected == i {
			prefix = "> "
		}
		line := prefix + mark + ansi.Wrap(item.Label, width-8, "")
		line = strings.ReplaceAll(line, "\n", "\n      ")
		if m.focus == 0 && m.selected == i {
			line = primary.Render(line)
		}
		lines = append(lines, line)
	}
	cancel, proceed := "[ Cancel ]", "[ "+m.opts.ConfirmLabel+" ]"
	if m.focus == 1 {
		cancel = primary.Render("> " + cancel)
	}
	if m.focus == 2 {
		proceed = primary.Render("> " + proceed)
	}
	body := primary.Render(m.opts.Title) + "\n\n" + strings.Join(lines, "\n")
	if m.opts.Description != "" {
		body += "\n\n" + m.opts.Description
	}
	body += "\n\n" + cancel + "    " + proceed + "\n\n↑↓ Select · Space Toggle · Tab Actions · Enter Confirm · Esc Cancel"
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary).Padding(0, 1).Width(width+2).Render(body) + "\n"
}
