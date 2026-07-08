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
)

const (
	taskProgressStackWidth  = 110
	taskProgressFrameHeight = MinTUIHeight - 3
)

// TaskProgressEventType describes a lifecycle transition for one task progress
// row. Callers normally do not need to construct these values directly; use the
// TaskProgress helper methods passed to RunTaskProgress instead.
type TaskProgressEventType string

const (
	// TaskProgressStarted marks an item as actively running. Started items show
	// elapsed time and, when Expected is set, an expectation bar until that
	// duration is exceeded.
	TaskProgressStarted TaskProgressEventType = "started"

	// TaskProgressDone marks an item as complete. Done items count toward the
	// overall completion total and contribute their full expected duration to the
	// overall expectation bar.
	TaskProgressDone TaskProgressEventType = "done"

	// TaskProgressOmitted removes an item from the progress UI when the caller
	// determines that the phase does not apply to this run.
	TaskProgressOmitted TaskProgressEventType = "omitted"

	// TaskProgressFailed marks an item as failed and displays the event error in
	// the progress UI.
	TaskProgressFailed TaskProgressEventType = "failed"

	// TaskProgressSkipped marks an item as already satisfied, such as a reused
	// local server or existing VM image.
	TaskProgressSkipped TaskProgressEventType = "skipped"

	// TaskProgressInfo updates an item's message without changing its lifecycle
	// state.
	TaskProgressInfo TaskProgressEventType = "info"
)

// TaskProgressItem describes one row in a task progress UI. Items are rendered
// in the order provided to RunTaskProgress, and their Expected durations are
// summed to produce the overall expected-time bar.
type TaskProgressItem struct {
	// Key is the stable identifier used by events to update this row.
	Key string

	// Name is the user-facing row label shown in the progress UI.
	Name string

	// Exclude removes this item from the progress UI entirely. Excluded items do
	// not render and do not contribute to the overall expected-time bar.
	Exclude bool

	// Success is the message shown when this item completes successfully and the
	// completion event does not provide a more specific message.
	Success string

	// Expected is the usual duration for this task. It is used only to set user
	// expectations; it is not treated as actual work completion.
	Expected time.Duration

	// Timeout is the maximum wait duration for this task when the caller has one.
	// It is displayed as a hint after Expected is exceeded.
	Timeout time.Duration

	// Group optionally places this item under a logical group in wide progress
	// views. It does not affect task execution or expected-time calculations.
	Group string

	// Ready marks this item as a user-usable capability rather than internal
	// setup work. Ready items appear in the Ready section when complete.
	Ready bool
}

// TaskProgressOptions configures task progress rendering.
type TaskProgressOptions struct {
	Title     string
	Subtitle  string
	DoneTitle string
}

// TaskProgressEvent reports progress for one task progress item. The progress
// UI is intentionally event-driven so long-running operations can remain owned
// by their domain package while the command layer decides whether to render a
// TTY dashboard or line-oriented fallback.
type TaskProgressEvent struct {
	// Type is the lifecycle transition or message update being reported.
	Type TaskProgressEventType

	// Key identifies the item this event updates.
	Key string

	// Name can set or replace the item label when the event creates an item not
	// present in the initial item list.
	Name string

	// Message is an optional user-facing detail rendered next to the item.
	Message string

	// Err is displayed for failed items.
	Err error
}

// TaskProgress emits task progress events. Its methods are nil-safe so domain
// code can report progress unconditionally; callers that do not need a progress
// UI can leave the progress value nil.
type TaskProgress func(TaskProgressEvent)

// Started emits a task-started event for key. Timing metadata comes from the
// TaskProgressItem supplied to RunTaskProgress.
func (p TaskProgress) Started(key, name, message string) {
	if p == nil {
		return
	}
	p(TaskProgressEvent{Type: TaskProgressStarted, Key: key, Name: name, Message: message})
}

// Done emits a task-done event for key with an optional completion message.
func (p TaskProgress) Done(key, name, message string) {
	if p == nil {
		return
	}
	p(TaskProgressEvent{Type: TaskProgressDone, Key: key, Name: name, Message: message})
}

// Omitted emits a task-omitted event for key. Omitted items are hidden and do
// not contribute to the overall expected-time summary.
func (p TaskProgress) Omitted(key, name string) {
	if p == nil {
		return
	}
	p(TaskProgressEvent{Type: TaskProgressOmitted, Key: key, Name: name})
}

// Skipped emits a task-skipped event for key. Skipped items are rendered as
// satisfied and count as complete in the overall summary.
func (p TaskProgress) Skipped(key, name, message string) {
	if p == nil {
		return
	}
	p(TaskProgressEvent{Type: TaskProgressSkipped, Key: key, Name: name, Message: message})
}

// Failed emits a task-failed event for key and displays err in the progress UI.
func (p TaskProgress) Failed(key, name string, err error) {
	if p == nil {
		return
	}
	p(TaskProgressEvent{Type: TaskProgressFailed, Key: key, Name: name, Err: err})
}

// RunTaskProgress renders sequential task progress while run executes. The UI
// includes an overall expected-time bar plus per-item rows; both are expectation
// indicators, not exact work-completion percentages. It falls back to
// line-oriented output when stdout cannot support an interactive TUI. Small
// terminals still start the TUI so users see the resize prompt and can press p
// to switch to plain output if desired.
func RunTaskProgress(opts TaskProgressOptions, items []TaskProgressItem, run func(TaskProgress) error) error {
	items = includedTaskProgressItems(items)
	if !CanUseTUI(0, 0).OK {
		return runTextTaskProgress(items, run)
	}
	if opts.Title == "" {
		opts.Title = "Podplane"
	}
	if opts.Subtitle == "" {
		opts.Subtitle = opts.Title
	}
	if opts.DoneTitle == "" {
		opts.DoneTitle = opts.Subtitle
	}

	events := make(chan TaskProgressEvent, 64)
	done := make(chan taskProgressDoneMsg, 1)
	m := taskProgressModel{
		title:     opts.Title,
		subtitle:  opts.Subtitle,
		doneTitle: opts.DoneTitle,
		run:       run,
		events:    events,
		done:      done,
		items:     items,
		rows:      map[string]taskProgressRow{},
		tui:       true,
	}
	for _, item := range items {
		m.rows[item.Key] = taskProgressRow{item: item, status: "pending"}
	}
	finalModel, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return fmt.Errorf("error running task progress: %w", err)
	}
	if m, ok := finalModel.(taskProgressModel); ok {
		if !m.tui {
			return drainTextTaskProgress(items, events, done)
		}
		return m.err
	}
	return nil
}

// includedTaskProgressItems returns only items that should participate in the
// progress UI and expected-time totals.
func includedTaskProgressItems(items []TaskProgressItem) []TaskProgressItem {
	included := make([]TaskProgressItem, 0, len(items))
	for _, item := range items {
		if item.Exclude {
			continue
		}
		included = append(included, item)
	}
	return included
}

type taskProgressRow struct {
	item      TaskProgressItem
	status    string
	message   string
	seen      bool
	omitted   bool
	startedAt time.Time
	doneAt    time.Time
	err       error
}

type taskProgressModel struct {
	title        string
	subtitle     string
	doneTitle    string
	run          func(TaskProgress) error
	events       chan TaskProgressEvent
	done         chan taskProgressDoneMsg
	items        []TaskProgressItem
	rows         map[string]taskProgressRow
	err          error
	doneReceived bool
	closed       bool
	width        int
	height       int
	tui          bool
	animation    int
}

type taskProgressEventMsg TaskProgressEvent

type taskProgressDoneMsg struct {
	err error
}

type taskProgressClosedMsg struct{}

type taskProgressTickMsg struct{}

// Init starts the background task, event reader, and repaint ticker.
func (m taskProgressModel) Init() tea.Cmd {
	return tea.Batch(m.runCommand(), m.waitForEvent(), taskProgressTick())
}

// runCommand executes the caller's work and forwards emitted progress events
// into the Bubble Tea update loop.
func (m taskProgressModel) runCommand() tea.Cmd {
	return func() tea.Msg {
		progress := TaskProgress(func(event TaskProgressEvent) {
			select {
			case m.events <- event:
			default:
			}
		})
		err := m.run(progress)
		close(m.events)
		if m.done != nil {
			m.done <- taskProgressDoneMsg{err: err}
		}
		return taskProgressDoneMsg{err: err}
	}
}

// waitForEvent waits for the next progress event or reports that the event
// channel has closed.
func (m taskProgressModel) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.events
		if !ok {
			return taskProgressClosedMsg{}
		}
		return taskProgressEventMsg(event)
	}
}

// taskProgressTick schedules periodic repaints so elapsed-time bars update
// even when no new task events arrive.
func taskProgressTick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return taskProgressTickMsg{} })
}

// Update handles key input, task lifecycle events, repaint ticks, and task
// completion signals.
func (m taskProgressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.doneReceived && m.closed {
			return m, tea.Quit
		}
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("task progress cancelled")
			return m, tea.Quit
		}
		if msg.String() == "p" && m.tooSmall() {
			m.tui = false
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case taskProgressEventMsg:
		m.applyEvent(TaskProgressEvent(msg))
		return m, m.waitForEvent()
	case taskProgressTickMsg:
		if m.doneReceived && m.closed {
			m.animation++
			return m, taskProgressTick()
		}
		return m, taskProgressTick()
	case taskProgressDoneMsg:
		m.doneReceived = true
		m.err = msg.err
		if msg.err != nil {
			return m, tea.Quit
		}
		if m.closed {
			return m, nil
		}
		return m, nil
	case taskProgressClosedMsg:
		m.closed = true
		if m.doneReceived {
			if m.err != nil {
				return m, tea.Quit
			}
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

// applyEvent updates the row state for one progress event, creating a row when
// callers emit an event for a key that was not in the initial item list.
func (m *taskProgressModel) applyEvent(event TaskProgressEvent) {
	if event.Key == "" {
		return
	}
	row, ok := m.rows[event.Key]
	if !ok {
		row = taskProgressRow{item: TaskProgressItem{Key: event.Key, Name: event.Name}, status: "pending"}
		m.items = append(m.items, row.item)
	}
	row.seen = true
	switch event.Type {
	case TaskProgressStarted:
		row.status = "running"
		row.startedAt = time.Now()
		row.doneAt = time.Time{}
		row.err = nil
		if event.Message != "" {
			row.message = event.Message
		}
	case TaskProgressDone:
		row.status = "done"
		if row.startedAt.IsZero() {
			row.startedAt = time.Now()
		}
		row.doneAt = time.Now()
		row.message = event.Message
	case TaskProgressOmitted:
		row.omitted = true
	case TaskProgressFailed:
		row.status = "failed"
		row.err = event.Err
		row.doneAt = time.Now()
		if event.Message != "" {
			row.message = event.Message
		}
	case TaskProgressSkipped:
		row.status = "skipped"
		row.doneAt = time.Now()
		if event.Message != "" {
			row.message = event.Message
		}
	case TaskProgressInfo:
		// Message-only update.
		if event.Message != "" {
			row.message = event.Message
		}
	}
	m.rows[event.Key] = row
}

// View renders the task progress dashboard.
func (m taskProgressModel) View() string {
	if m.tooSmall() {
		return tooSmallTaskProgressView()
	}

	var b strings.Builder
	faintStyle := lipgloss.NewStyle().Faint(true)
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f87"))
	cardStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5865f2")).Padding(0, 1)

	current, total, _, _, remaining := m.overallProgress()
	var body strings.Builder
	if m.doneReceived && m.closed {
		body.WriteString(m.doneTitle)
		body.WriteString("\n\n")
		body.WriteString(renderRocketLaunch(m.animation))
		body.WriteString("\n\n")
		body.WriteString("Ready\n")
		body.WriteString("\n")
		for _, row := range m.readyRows() {
			fmt.Fprintf(&body, "• %s", row.item.Name)
			body.WriteString("\n")
		}
		body.WriteString("\n")
		fmt.Fprintf(&body, "Cluster started in %s\n", formatDuration(m.completedElapsed()))
	} else {
		body.WriteString(m.subtitle)
		body.WriteString("\n\n")
		if total > 0 {
			label := m.timeSummary(remaining)
			barWidth := 32
			if m.width > 0 && m.width < 90 {
				barWidth = 28
			}
			fmt.Fprintf(&body, "%s  %d%%\n", renderBar(int64(current), int64(total), barWidth), progressPercent(current, total))
			body.WriteString(faintStyle.Render(label))
			body.WriteString("\n\n")
		}
		body.WriteString("In progress\n")
		inProgress := m.inProgressRows()
		if len(inProgress) == 0 {
			body.WriteString(faintStyle.Render("Preparing the next step…"))
			body.WriteString("\n")
		} else {
			for _, row := range inProgress {
				line := renderCompactProgressRow(row)
				if row.status == "failed" {
					line = errorStyle.Render(line)
				}
				body.WriteString(line)
				body.WriteString("\n")
			}
		}
		body.WriteString("\n")
		body.WriteString("Ready\n")
		ready := m.readyRows()
		if len(ready) == 0 {
			body.WriteString(faintStyle.Render("Nothing is ready yet"))
			body.WriteString("\n")
		} else {
			for _, row := range ready {
				fmt.Fprintf(&body, "✓ %s", row.item.Name)
				body.WriteString("\n")
			}
		}
	}

	content := body.String()
	if m.width >= taskProgressStackWidth {
		content = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(58).Render(body.String()), m.renderStack())
	}
	if m.doneReceived && m.closed {
		content += "\n" + faintStyle.Render("Press any key to continue")
	}

	card := cardStyle.Width(m.cardWidth()).Height(taskProgressFrameHeight).Render(content)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card)
	}
	b.WriteString("\n")
	b.WriteString(card)
	b.WriteString("\n")
	return b.String()
}

// tooSmall reports whether the current terminal size is below the supported
// minimum for the full task progress view.
func (m taskProgressModel) tooSmall() bool {
	return (m.width > 0 && m.width < MinTUIWidth) || (m.height > 0 && m.height < MinTUIHeight)
}

// tooSmallTaskProgressView renders the compact undersized-terminal message.
func tooSmallTaskProgressView() string {
	return fmt.Sprintf("Podplane local start is still running.\n\nThe installer view needs at least %dx%d.\nResize your terminal to continue.\n\nPress p to disable the TUI and continue with plain output.\n", MinTUIWidth, MinTUIHeight)
}

// renderRocketLaunch renders the completed-state launch animation shown while
// the installer waits for the user to leave the full-screen view.
func renderRocketLaunch(frame int) string {
	frames := []string{
		"·   ✦   ·   ✧ \n  ·   ·   ✦  ·\n✧   ·   ·   · \n  ·   ✦   ·   \n 🚀  ·   ✦  · ",
		"·   ✧   ·   ✦ \n  ·   ·   ✦  ·\n✦   ·   ·   · \n    🚀✧   ·   \n ·   ·   ✦  · ",
		"·   ✦   ·   ✦ \n  ·   ·   ✧  ·\n✦     🚀·   · \n  ·   ✦   ·   \n ·   ·   ✧  · ",
		"·   ✦   ·   ✧ \n  ·     🚀✦  ·\n✧   ·   ·   · \n  ·   ✦   ·   \n ·   ·   ✦  · ",
		"·   ✧     🚀✦ \n  ·   ·   ✦  ·\n✦   ·   ·   · \n  ·   ✧   ·   \n 🚀  ·   ✦  · ",
	}
	return frames[frame%len(frames)]
}

// cardWidth returns the outer card width appropriate for the current terminal.
func (m taskProgressModel) cardWidth() int {
	if m.width < taskProgressStackWidth {
		return MinTUIWidth
	}
	width := m.width - 8
	if m.width >= taskProgressStackWidth {
		if width > 112 {
			return 112
		}
		return width
	}
	return MinTUIWidth
}

// timeSummary renders the elapsed/remaining/overtime summary for the main
// progress card.
func (m taskProgressModel) timeSummary(remaining time.Duration) string {
	elapsed := m.wallElapsed()
	if m.allDone() {
		return fmt.Sprintf("took %s", formatDuration(elapsed))
	}
	if remaining < 0 {
		overtimeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f87"))
		return fmt.Sprintf("elapsed %s · %s", formatDuration(elapsed), overtimeStyle.Render("+"+formatDuration(-remaining)))
	}
	return fmt.Sprintf("elapsed %s · about %s left", formatDuration(elapsed), formatDuration(remaining))
}

// wallElapsed returns elapsed wall-clock time since the first visible task
// started.
func (m taskProgressModel) wallElapsed() time.Duration {
	var start time.Time
	for _, row := range m.visibleRows() {
		if !row.startedAt.IsZero() && (start.IsZero() || row.startedAt.Before(start)) {
			start = row.startedAt
		}
	}
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}

// completedElapsed returns the frozen elapsed duration for the completed view.
func (m taskProgressModel) completedElapsed() time.Duration {
	var start time.Time
	var end time.Time
	for _, row := range m.visibleRows() {
		if !row.startedAt.IsZero() && (start.IsZero() || row.startedAt.Before(start)) {
			start = row.startedAt
		}
		if !row.doneAt.IsZero() && row.doneAt.After(end) {
			end = row.doneAt
		}
	}
	if start.IsZero() || end.IsZero() {
		return m.wallElapsed()
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

// allDone reports whether all visible non-ready work items are complete.
func (m taskProgressModel) allDone() bool {
	for _, row := range m.visibleRows() {
		if row.item.Ready || row.item.Expected <= 0 {
			continue
		}
		if row.status != "done" && row.status != "skipped" {
			return false
		}
	}
	return len(m.visibleRows()) > 0
}

// inProgressRows returns currently active work items for the simple view.
func (m taskProgressModel) inProgressRows() []taskProgressRow {
	rows := []taskProgressRow{}
	for _, row := range m.visibleRows() {
		if row.item.Ready {
			continue
		}
		if row.status == "running" || row.status == "failed" {
			rows = append(rows, row)
		}
	}
	return rows
}

// readyRows returns user-facing readiness items that have completed.
func (m taskProgressModel) readyRows() []taskProgressRow {
	rows := []taskProgressRow{}
	for _, row := range m.visibleRows() {
		if !row.item.Ready {
			continue
		}
		if row.status == "done" || row.status == "skipped" {
			rows = append(rows, row)
		}
	}
	return rows
}

// renderStack renders the optional wide-screen stack/timing column.
func (m taskProgressModel) renderStack() string {
	var b strings.Builder
	faintStyle := lipgloss.NewStyle().Faint(true)
	b.WriteString("Stack")
	b.WriteString(faintStyle.Render("                         time"))
	b.WriteString("\n")
	lastGroup := ""
	for _, row := range m.visibleRows() {
		if row.item.Ready || row.omitted || row.status == "pending" {
			continue
		}
		group := row.item.Group
		if group != "" && group != lastGroup {
			groupRow := row
			groupRow.item.Name = group
			groupRow.item.Group = ""
			b.WriteString(renderStackLine(groupRow, false))
			b.WriteString("\n")
			lastGroup = group
		}
		b.WriteString(renderStackLine(row, group != ""))
		b.WriteString("\n")
	}
	return b.String()
}

// renderCompactProgressRow renders one active row in the simple view.
func renderCompactProgressRow(row taskProgressRow) string {
	marker := taskProgressMarker(row)
	name := row.item.Name
	if row.message != "" {
		name = fmt.Sprintf("%s — %s", name, row.message)
	}
	return fmt.Sprintf("%s %s", marker, name)
}

// renderStackLine renders one row in the wide stack column.
func renderStackLine(row taskProgressRow, indent bool) string {
	name := row.item.Name
	if indent {
		name = "  " + name
	}
	return fmt.Sprintf("%s %-24s %7s", taskProgressMarker(row), name, renderTaskTiming(row))
}

// taskProgressMarker returns the symbolic status marker for a task row.
func taskProgressMarker(row taskProgressRow) string {
	switch row.status {
	case "done", "skipped":
		return "✓"
	case "failed":
		return "✗"
	case "running":
		return "◐"
	default:
		return "…"
	}
}

// renderTaskTiming renders the subtle status-aware timing value for a task row.
func renderTaskTiming(row taskProgressRow) string {
	if row.startedAt.IsZero() {
		return ""
	}
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3ddc84"))
	runningStyle := lipgloss.NewStyle().Faint(true)
	overtimeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f87"))
	if row.status == "done" || row.status == "skipped" {
		elapsed := row.doneAt.Sub(row.startedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		return successStyle.Render(formatDuration(elapsed))
	}
	if row.status == "running" && row.item.Expected > 0 {
		elapsed := time.Since(row.startedAt)
		if elapsed > row.item.Expected {
			return overtimeStyle.Render("+" + formatDuration(elapsed-row.item.Expected))
		}
		return runningStyle.Render(fmt.Sprintf("%s/%s", formatDuration(elapsed), formatDuration(row.item.Expected)))
	}
	if row.status == "running" {
		return runningStyle.Render(formatDuration(time.Since(row.startedAt)))
	}
	return ""
}

// progressPercent renders current/total progress as an integer percentage.
func progressPercent(current, total time.Duration) int {
	if total <= 0 {
		return 0
	}
	percent := int((current * 100) / total)
	if percent > 100 {
		return 100
	}
	if percent < 0 {
		return 0
	}
	return percent
}

// visibleRows returns rows that should be rendered. Rows that have not emitted
// any event yet are shown only while they are still ahead of the current flow;
// this hides optional phases skipped by the caller, such as VM image creation
// when starting an existing VM.
func (m taskProgressModel) visibleRows() []taskProgressRow {
	lastSeen := -1
	for i, item := range m.items {
		row := m.rows[item.Key]
		if row.seen && !row.omitted {
			lastSeen = i
		}
	}
	if lastSeen < 0 {
		return nil
	}
	rows := make([]taskProgressRow, 0, len(m.items))
	for i, item := range m.items {
		row := m.rows[item.Key]
		if row.omitted {
			continue
		}
		if row.seen || i > lastSeen {
			rows = append(rows, row)
		}
	}
	return rows
}

// overallProgress returns the expected-time progress totals, completion counts,
// and the monotonic displayed remaining time.
func (m taskProgressModel) overallProgress() (time.Duration, time.Duration, int, int, time.Duration) {
	var total time.Duration
	var completeExpected time.Duration
	var complete int
	var tracked int
	var latestCompletedAt time.Time
	for _, item := range m.items {
		row := m.rows[item.Key]
		if row.omitted {
			continue
		}
		if row.item.Expected <= 0 {
			continue
		}
		tracked++
		total += row.item.Expected
		if row.status == "done" || row.status == "skipped" {
			complete++
			completeExpected += row.item.Expected
			if !row.doneAt.IsZero() && (latestCompletedAt.IsZero() || row.doneAt.After(latestCompletedAt)) {
				latestCompletedAt = row.doneAt
			}
		}
	}
	remaining := total - m.wallElapsed()
	plannedRemaining := total - completeExpected
	if !latestCompletedAt.IsZero() {
		plannedRemaining -= time.Since(latestCompletedAt)
	}
	if plannedRemaining < remaining {
		remaining = plannedRemaining
	}
	current := total - remaining
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}
	return current, total, complete, tracked, remaining
}

// formatDuration renders short human-readable durations for progress rows.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

// runTextTaskProgress is the non-TTY fallback for task progress.
func runTextTaskProgress(items []TaskProgressItem, run func(TaskProgress) error) error {
	successByKey := map[string]string{}
	for _, item := range items {
		if item.Success != "" {
			successByKey[item.Key] = item.Success
		}
	}
	return run(TaskProgress(func(event TaskProgressEvent) {
		switch event.Type {
		case TaskProgressStarted:
			_, _ = fmt.Fprintf(os.Stdout, "%s...\n", event.Name)
		case TaskProgressDone:
			message := event.Message
			if message == "" {
				message = successByKey[event.Key]
			}
			if message == "" {
				message = "done"
			}
			_, _ = fmt.Fprintf(os.Stdout, "✓ %s %s\n", event.Name, message)
		case TaskProgressSkipped:
			if event.Message != "" {
				_, _ = fmt.Fprintf(os.Stdout, "✓ %s %s\n", event.Name, event.Message)
			}
		case TaskProgressOmitted:
			// Omitted items intentionally produce no non-TTY output.
		case TaskProgressFailed:
			if event.Err != nil {
				_, _ = fmt.Fprintf(os.Stdout, "❌ %s failed: %v\n", event.Name, event.Err)
			}
		case TaskProgressInfo:
			if event.Message != "" {
				_, _ = fmt.Fprintln(os.Stdout, event.Message)
			}
		}
	}))
}

// drainTextTaskProgress continues rendering task events as line-oriented output
// after a running TUI has been disabled.
func drainTextTaskProgress(items []TaskProgressItem, events <-chan TaskProgressEvent, done <-chan taskProgressDoneMsg) error {
	successByKey := map[string]string{}
	for _, item := range items {
		if item.Success != "" {
			successByKey[item.Key] = item.Success
		}
	}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			printTextTaskProgressEvent(successByKey, event)
		case result := <-done:
			if events != nil {
				for event := range events {
					printTextTaskProgressEvent(successByKey, event)
				}
			}
			return result.err
		}
	}
}

// printTextTaskProgressEvent renders one task event in the fallback text UI.
func printTextTaskProgressEvent(successByKey map[string]string, event TaskProgressEvent) {
	switch event.Type {
	case TaskProgressStarted:
		_, _ = fmt.Fprintf(os.Stdout, "%s...\n", event.Name)
	case TaskProgressDone:
		message := event.Message
		if message == "" {
			message = successByKey[event.Key]
		}
		if message == "" {
			message = "done"
		}
		_, _ = fmt.Fprintf(os.Stdout, "✓ %s %s\n", event.Name, message)
	case TaskProgressSkipped:
		if event.Message != "" {
			_, _ = fmt.Fprintf(os.Stdout, "✓ %s %s\n", event.Name, event.Message)
		}
	case TaskProgressOmitted:
		// Omitted items intentionally produce no fallback output.
	case TaskProgressFailed:
		if event.Err != nil {
			_, _ = fmt.Fprintf(os.Stdout, "❌ %s failed: %v\n", event.Name, event.Err)
		}
	case TaskProgressInfo:
		if event.Message != "" {
			_, _ = fmt.Fprintln(os.Stdout, event.Message)
		}
	}
}
