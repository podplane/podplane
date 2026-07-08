// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/podplane/podplane/internal/deps"

	tea "github.com/charmbracelet/bubbletea"
)

const downloadProgressBarWidth = 28
const defaultDownloadProgressVisibleItems = 8

// RunDownloadProgress renders deps download progress while run executes.
// It falls back to plain line-oriented output when stdout is not a terminal.
func RunDownloadProgress(title string, run func(func(deps.DownloadEvent)) error) error {
	if !CanUseTUI(0, 0).OK {
		return runTextDownloadProgress(run)
	}

	events := make(chan deps.DownloadEvent, 256)
	m := downloadProgressModel{
		title:  title,
		run:    run,
		events: events,
		items:  map[string]downloadProgressItem{},
	}
	finalModel, err := tea.NewProgram(m).Run()
	if err != nil {
		return fmt.Errorf("error running download progress: %w", err)
	}
	if m, ok := finalModel.(downloadProgressModel); ok {
		return m.err
	}
	return nil
}

type downloadProgressItem struct {
	Name    string
	Path    string
	Status  deps.DownloadEventType
	Current int64
	Total   int64
	Err     error
	Order   int
	Updated time.Time
}

type downloadProgressModel struct {
	title        string
	run          func(func(deps.DownloadEvent)) error
	events       chan deps.DownloadEvent
	items        map[string]downloadProgressItem
	order        []string
	message      string
	err          error
	doneReceived bool
	closed       bool
	height       int
}

type downloadProgressEventMsg deps.DownloadEvent

type downloadProgressDoneMsg struct {
	err error
}

type downloadProgressClosedMsg struct{}

func (m downloadProgressModel) Init() tea.Cmd {
	return tea.Batch(m.runCommand(), m.waitForEvent())
}

func (m downloadProgressModel) runCommand() tea.Cmd {
	return func() tea.Msg {
		err := m.run(func(event deps.DownloadEvent) {
			select {
			case m.events <- event:
			default:
			}
		})
		close(m.events)
		return downloadProgressDoneMsg{err: err}
	}
}

func (m downloadProgressModel) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.events
		if !ok {
			return downloadProgressClosedMsg{}
		}
		return downloadProgressEventMsg(event)
	}
}

func (m downloadProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			m.err = fmt.Errorf("download cancelled")
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case downloadProgressEventMsg:
		m.applyEvent(deps.DownloadEvent(msg))
		return m, m.waitForEvent()
	case downloadProgressDoneMsg:
		m.doneReceived = true
		m.err = msg.err
		if m.closed {
			return m, tea.Quit
		}
		return m, nil
	case downloadProgressClosedMsg:
		m.closed = true
		if m.doneReceived {
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

func (m *downloadProgressModel) applyEvent(event deps.DownloadEvent) {
	if event.Type == deps.DownloadEventStatus {
		m.message = event.Message
		return
	}
	if event.Name == "" {
		return
	}

	item, ok := m.items[event.Name]
	if !ok {
		item = downloadProgressItem{Name: event.Name, Order: len(m.order)}
		m.order = append(m.order, event.Name)
	}
	item.Status = event.Type
	item.Updated = time.Now()
	if event.Path != "" {
		item.Path = event.Path
	}
	if event.Total > 0 {
		item.Total = event.Total
	}
	if event.Current > 0 || event.Type == deps.DownloadEventProgress {
		item.Current = event.Current
	}
	if event.Err != nil {
		item.Err = event.Err
	}
	if event.Type == deps.DownloadEventDone || event.Type == deps.DownloadEventCached {
		if item.Total == 0 {
			item.Total = item.Current
		}
		if item.Current == 0 {
			item.Current = item.Total
		}
	}
	m.items[event.Name] = item
}

func (m downloadProgressModel) View() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorWhite).Padding(0, 1)
	faintStyle := lipgloss.NewStyle().Faint(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f87"))

	b.WriteString("\n")
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n\n")
	if m.message != "" {
		b.WriteString(m.message)
		b.WriteString("\n\n")
	}

	current, total := m.overallProgress()
	fmt.Fprintf(&b, "Overall  %s  %s\n\n", renderBar(current, total, downloadProgressBarWidth), renderBytesProgress(current, total))

	visibleItems, hiddenItems := m.visibleItems()
	for _, item := range visibleItems {
		line := m.renderItem(item)
		if item.Err != nil {
			line = errorStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if hiddenItems > 0 {
		b.WriteString(faintStyle.Render(fmt.Sprintf("… %d items hidden", hiddenItems)))
		b.WriteString("\n")
	}

	if len(m.order) == 0 {
		b.WriteString(faintStyle.Render("Waiting for downloads to start..."))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(faintStyle.Render(m.summary()))
	b.WriteString("\n")
	return b.String()
}

func (m downloadProgressModel) visibleItems() ([]downloadProgressItem, int) {
	maxItems := m.maxVisibleItems()
	if maxItems <= 0 {
		maxItems = defaultDownloadProgressVisibleItems
	}
	visible := make([]downloadProgressItem, 0, maxItems)
	shown := map[string]bool{}

	appendMatching := func(match func(downloadProgressItem) bool, reverse bool) {
		if len(visible) >= maxItems {
			return
		}
		if reverse {
			for i := len(m.order) - 1; i >= 0 && len(visible) < maxItems; i-- {
				name := m.order[i]
				if shown[name] {
					continue
				}
				item := m.items[name]
				if !match(item) {
					continue
				}
				visible = append(visible, item)
				shown[name] = true
			}
			return
		}
		for _, name := range m.order {
			if len(visible) >= maxItems || shown[name] {
				continue
			}
			item := m.items[name]
			if !match(item) {
				continue
			}
			visible = append(visible, item)
			shown[name] = true
		}
	}

	appendMatching(func(item downloadProgressItem) bool { return item.Status == deps.DownloadEventFailed }, false)
	appendMatching(func(item downloadProgressItem) bool {
		return item.Status == deps.DownloadEventProgress || item.Status == deps.DownloadEventStarted
	}, false)
	appendMatching(func(item downloadProgressItem) bool {
		return item.Status == deps.DownloadEventQueued || item.Status == deps.DownloadEventChecking
	}, false)
	appendMatching(func(item downloadProgressItem) bool {
		return item.Status == deps.DownloadEventDone || item.Status == deps.DownloadEventCached
	}, true)

	return visible, len(m.order) - len(visible)
}

func (m downloadProgressModel) maxVisibleItems() int {
	if m.height <= 0 {
		return defaultDownloadProgressVisibleItems
	}
	reservedRows := 9
	if m.message != "" {
		reservedRows += 2
	}
	maxItems := m.height - reservedRows
	if maxItems < 3 {
		return 3
	}
	return maxItems
}

func (m downloadProgressModel) renderItem(item downloadProgressItem) string {
	name := item.Name
	if item.Path != "" && name == "" {
		name = filepath.Base(item.Path)
	}
	status := itemStatusLabel(item)
	if item.Status == deps.DownloadEventProgress || item.Status == deps.DownloadEventStarted {
		return fmt.Sprintf("%s %-24s %s %s", status, name, renderBar(item.Current, item.Total, 14), renderBytesProgress(item.Current, item.Total))
	}
	if item.Err != nil {
		return fmt.Sprintf("%s %-24s %v", status, name, item.Err)
	}
	if item.Total > 0 {
		return fmt.Sprintf("%s %-24s %s", status, name, formatBytes(item.Total))
	}
	return fmt.Sprintf("%s %-24s %s", status, name, itemStateText(item.Status))
}

func (m downloadProgressModel) overallProgress() (int64, int64) {
	var current int64
	var total int64
	for _, item := range m.items {
		if item.Total <= 0 {
			continue
		}
		total += item.Total
		if item.Current > item.Total {
			current += item.Total
		} else {
			current += item.Current
		}
	}
	return current, total
}

func (m downloadProgressModel) summary() string {
	var active, complete, cached, failed, queued int
	for _, item := range m.items {
		switch item.Status {
		case deps.DownloadEventProgress, deps.DownloadEventStarted:
			active++
		case deps.DownloadEventDone:
			complete++
		case deps.DownloadEventCached:
			complete++
			cached++
		case deps.DownloadEventFailed:
			failed++
		case deps.DownloadEventQueued, deps.DownloadEventChecking:
			queued++
		}
	}
	return fmt.Sprintf("%d required, %d active, %d complete, %d cached, %d queued, %d failed", len(m.items), active, complete, cached, queued, failed)
}

func itemStatusLabel(item downloadProgressItem) string {
	switch item.Status {
	case deps.DownloadEventCached:
		return "✓"
	case deps.DownloadEventDone:
		return "✓"
	case deps.DownloadEventProgress, deps.DownloadEventStarted:
		return "⟳"
	case deps.DownloadEventFailed:
		return "✗"
	case deps.DownloadEventChecking:
		return "…"
	default:
		return "•"
	}
}

func itemStateText(status deps.DownloadEventType) string {
	switch status {
	case deps.DownloadEventChecking:
		return "checking cache"
	case deps.DownloadEventQueued:
		return "queued"
	case deps.DownloadEventStarted:
		return "starting"
	case deps.DownloadEventFailed:
		return "failed"
	default:
		return string(status)
	}
}

func renderBar(current, total int64, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 {
		return "[" + strings.Repeat("░", width) + "]"
	}
	filled := int(float64(current) / float64(total) * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func renderBytesProgress(current, total int64) string {
	if total <= 0 {
		return formatBytes(current)
	}
	return fmt.Sprintf("%s / %s", formatBytes(current), formatBytes(total))
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func runTextDownloadProgress(run func(func(deps.DownloadEvent)) error) error {
	return run(func(event deps.DownloadEvent) {
		switch event.Type {
		case deps.DownloadEventStatus:
			if event.Message != "" {
				_, _ = fmt.Fprintln(os.Stdout, event.Message)
			}
		case deps.DownloadEventCached:
			name := event.Name
			if event.Path != "" {
				name = filepath.Base(event.Path)
			}
			_, _ = fmt.Fprintf(os.Stdout, "Found %s in cache.\n", name)
		case deps.DownloadEventStarted:
			_, _ = fmt.Fprintf(os.Stdout, "Downloading %s...\n", event.Name)
		case deps.DownloadEventDone:
			_, _ = fmt.Fprintf(os.Stdout, "Downloaded %s.\n", event.Name)
		case deps.DownloadEventFailed:
			_, _ = fmt.Fprintf(os.Stdout, "Failed %s: %v\n", event.Name, event.Err)
		}
	})
}
