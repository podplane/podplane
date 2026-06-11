// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

const statusProgressPollInterval = time.Second

// StatusProgressItem is one row in a status progress UI.
type StatusProgressItem struct {
	Key  string
	Name string
	Kind string
}

// StatusProgressStatus is the current observed status for one progress item.
type StatusProgressStatus struct {
	Exists  bool
	Ready   bool
	Status  string
	Message string
}

// RunStatusProgress renders status progress for a set of items. It polls once,
// then sleeps for one second between subsequent polls. The UI exits once every
// required item is Ready; non-required items may continue reconciling in the
// background.
func RunStatusProgress(title string, items, required []StatusProgressItem, poll func() (map[string]StatusProgressStatus, error)) error {
	if len(items) == 0 {
		return nil
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return runTextStatusProgress(items, required, poll)
	}
	m := statusProgressModel{
		title:    title,
		items:    items,
		required: required,
		poll:     poll,
	}
	finalModel, err := tea.NewProgram(m).Run()
	if err != nil {
		return fmt.Errorf("error running status progress: %w", err)
	}
	if m, ok := finalModel.(statusProgressModel); ok {
		return m.err
	}
	return nil
}

type statusProgressModel struct {
	title     string
	items     []StatusProgressItem
	required  []StatusProgressItem
	poll      func() (map[string]StatusProgressStatus, error)
	statuses  map[string]StatusProgressStatus
	err       error
	done      bool
	height    int
	pollCount int
}

type statusProgressPollMsg struct {
	statuses map[string]StatusProgressStatus
	err      error
}

type statusProgressTickMsg struct{}

// Init starts the first status poll immediately.
func (m statusProgressModel) Init() tea.Cmd {
	return m.pollCommand()
}

// Update handles terminal input, poll results, and delayed poll ticks.
func (m statusProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.err = fmt.Errorf("status progress cancelled")
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case statusProgressPollMsg:
		m.pollCount++
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.statuses = msg.statuses
		if statusProgressRequiredReady(m.statuses, m.required) {
			m.done = true
			return m, tea.Quit
		}
		return m, tea.Tick(statusProgressPollInterval, func(time.Time) tea.Msg { return statusProgressTickMsg{} })
	case statusProgressTickMsg:
		return m, m.pollCommand()
	}
	return m, nil
}

// pollCommand runs one status poll and reports the result back to Bubble Tea.
func (m statusProgressModel) pollCommand() tea.Cmd {
	return func() tea.Msg {
		statuses, err := m.poll()
		return statusProgressPollMsg{statuses: statuses, err: err}
	}
}

// View renders the status progress UI.
func (m statusProgressModel) View() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorWhite).Padding(0, 1)
	faintStyle := lipgloss.NewStyle().Faint(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f87"))

	b.WriteString("\n")
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n\n")
	if m.pollCount == 0 {
		b.WriteString(faintStyle.Render("Checking component HelmReleases..."))
		b.WriteString("\n")
		return b.String()
	}

	visible, hidden := m.visibleItems()
	for _, item := range visible {
		status := m.statuses[item.Key]
		line := renderStatusProgressLine(item, status, statusProgressItemRequired(item, m.required))
		if status.Exists && !status.Ready && strings.Contains(strings.ToLower(status.Status), "fail") {
			line = errorStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if hidden > 0 {
		b.WriteString(faintStyle.Render(fmt.Sprintf("… %d items hidden", hidden)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(faintStyle.Render(statusProgressSummary(m.statuses, m.items, m.required)))
	b.WriteString("\n")
	return b.String()
}

// visibleItems returns the install items that fit in the current terminal
// height and the count hidden below the fold.
func (m statusProgressModel) visibleItems() ([]StatusProgressItem, int) {
	maxItems := m.maxVisibleItems()
	if len(m.items) <= maxItems {
		return m.items, 0
	}
	return m.items[:maxItems], len(m.items) - maxItems
}

// maxVisibleItems returns how many dependency rows can be displayed.
func (m statusProgressModel) maxVisibleItems() int {
	if m.height <= 0 {
		return 10
	}
	maxItems := m.height - 7
	if maxItems < 3 {
		return 3
	}
	return maxItems
}

// runTextStatusProgress is the non-TTY fallback for status progress.
func runTextStatusProgress(items, required []StatusProgressItem, poll func() (map[string]StatusProgressStatus, error)) error {
	for {
		statuses, err := poll()
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, statusProgressSummary(statuses, items, required))
		if statusProgressRequiredReady(statuses, required) {
			return nil
		}
		time.Sleep(statusProgressPollInterval)
	}
}

// renderStatusProgressLine renders one progress row in the TUI.
func renderStatusProgressLine(item StatusProgressItem, status StatusProgressStatus, required bool) string {
	marker := "⟳"
	state := status.Status
	message := status.Message
	if !status.Exists {
		marker = "…"
		if state == "" {
			state = "Pending"
		}
		if message == "" {
			message = "waiting for item to be created"
		}
	} else if status.Ready {
		marker = "✓"
	}
	requiredLabel := ""
	if required {
		requiredLabel = " (required)"
	}
	if message == "" {
		return fmt.Sprintf("%s %-24s %-8s %s%s", marker, item.Name, item.Kind, state, requiredLabel)
	}
	return fmt.Sprintf("%s %-24s %-8s %s — %s%s", marker, item.Name, item.Kind, state, message, requiredLabel)
}

// statusProgressSummary renders aggregate readiness counts.
func statusProgressSummary(statuses map[string]StatusProgressStatus, items, required []StatusProgressItem) string {
	var ready, requiredReady int
	for _, item := range items {
		if statuses[item.Key].Ready {
			ready++
		}
	}
	for _, item := range required {
		if statuses[item.Key].Ready {
			requiredReady++
		}
	}
	if len(required) == 0 {
		return fmt.Sprintf("%d/%d total items ready", ready, len(items))
	}
	return fmt.Sprintf("%d/%d total items ready, %d/%d required items ready", ready, len(items), requiredReady, len(required))
}

// statusProgressItemRequired reports whether an item must be ready before the
// progress UI can exit.
func statusProgressItemRequired(item StatusProgressItem, required []StatusProgressItem) bool {
	for _, req := range required {
		if req.Key == item.Key {
			return true
		}
	}
	return false
}

// statusProgressRequiredReady reports whether every required item is ready.
func statusProgressRequiredReady(statuses map[string]StatusProgressStatus, required []StatusProgressItem) bool {
	for _, item := range required {
		if !statuses[item.Key].Ready {
			return false
		}
	}
	return true
}
