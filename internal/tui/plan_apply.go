// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/podplane/podplane/internal/tfexec"
)

// PlanAndApplyOptions supplies the stack identity and command to retry with a new plan.
type PlanAndApplyOptions struct {
	Title, Subtitle, Resume string
	AutoApprove             bool
}

// PlanAndApplyRun owns execution, sending events and requesting approval of a saved plan.
type PlanAndApplyRun func(context.Context, func(tfexec.Event), func(tfexec.Plan) (bool, error)) error

// planAndApplyReview pauses execution until the renderer answers the saved plan.
type planAndApplyReview struct {
	plan   tfexec.Plan
	answer chan bool
}

// planAndApplyDone reports that execution and engine cleanup have finished.
type planAndApplyDone struct{ err error }

// planAndApplyTick advances elapsed times and drains queued engine events.
type planAndApplyTick time.Time

// planAndApplyCopied reports the result of an asynchronous clipboard write.
type planAndApplyCopied struct{ err error }

// logSelection holds a frozen viewport and inclusive terminal-cell selection.
type logSelection struct {
	lines             []string
	width, start, end int
	dragging          bool
}

// text extracts selected characters without terminal styling or panel borders.
func (s logSelection) text() string {
	first, last := min(s.start, s.end), max(s.start, s.end)
	var lines []string
	for row := first / s.width; row <= last/s.width; row++ {
		from, to := 0, ansi.StringWidth(s.lines[row])
		if row == first/s.width {
			from = first % s.width
		}
		if row == last/s.width {
			to = min(to, last%s.width+1)
		}
		lines = append(lines, ansi.Cut(s.lines[row], from, max(from, to)))
	}
	return strings.Join(lines, "\n")
}

// copyPlanAndApplyLogs bounds clipboard writes and reports failures to the model.
func copyPlanAndApplyLogs(text string) tea.Cmd {
	return func() tea.Msg {
		if len(text) > 4*1024*1024 {
			return planAndApplyCopied{fmt.Errorf("logs exceed 4 MiB; select a smaller portion")}
		}
		return planAndApplyCopied{clipboard.WriteAll(text)}
	}
}

// RunPlanAndApply presents the same execution in TUI or Inline mode.
// Switching renderers never restarts execution or implicitly approves it.
func RunPlanAndApply(ctx context.Context, opts PlanAndApplyOptions, run PlanAndApplyRun) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	events := make(chan tea.Msg, 64)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer close(events)
		err := run(ctx, func(e tfexec.Event) { events <- e }, func(p tfexec.Plan) (bool, error) {
			r := planAndApplyReview{plan: p, answer: make(chan bool, 1)}
			events <- r
			select {
			case ok := <-r.answer:
				return ok, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		})
		events <- planAndApplyDone{err}
	}()
	m := planAndApplyModel{opts: opts, events: events, cancel: cancel, stage: "Init", started: time.Now(), rows: map[string]*planAndApplyRow{}, hideCompleted: true}
	if CanUseTUI(0, 0).OK && term.IsTerminal(int(os.Stdin.Fd())) && !flowInline(ctx) {
		final, err := Run(ctx, m)
		if err != nil {
			cancel()
			// Continue draining: the engine must finish cleanup before we return.
			// The dashboard may already have consumed the completion event.
			for range events {
			}
			<-finished
			return err
		}
		m = final.(planAndApplyModel)
		// Preserve diagnostics after leaving the alternate screen, including
		// recovery instructions that must not disappear with the dashboard.
		if m.err != nil || m.inlineMode {
			for _, line := range m.logs {
				FlowSummary(ctx, line)
			}
		} else {
			for _, diagnostic := range m.diagnostics {
				FlowSummary(ctx, diagnostic)
			}
		}
	}
	if !m.done {
		fmt.Printf("%s\n%s\n%s\n", opts.Title, opts.Subtitle, m.status())
		if len(m.plan.Changes) > 0 {
			complete, active, failed := m.counts()
			fmt.Printf("%d/%d complete · %d active · %d failed\n", complete, len(m.plan.Changes), active, failed)
		}
		if m.review != nil {
			m.inlineReview(ctx)
		}
		for !m.done {
			msg := <-events
			m.accept(msg)
			switch e := msg.(type) {
			case tfexec.Event:
				if e.Stage != "" {
					fmt.Println(e.Stage)
				}
				if e.Message != "" {
					fmt.Println(ansi.Strip(e.Message))
				}
			case planAndApplyReview:
				m.inlineReview(ctx)
			}
		}
	}
	<-finished
	_, _ = fmt.Fprintln(FlowOutput(ctx), m.status())
	if m.err != nil {
		FlowSummary(ctx, "Inspect the engine diagnostics above, including any state recovery instructions.")
		if m.stage == "Apply" {
			FlowSummary(ctx, "Applied changes have not been rolled back. Do not reuse the old plan.")
		}
		FlowSummary(ctx, "After resolving the error, create and review a new plan with:\n  "+opts.Resume)
	}
	return m.err
}

// planAndApplyRow tracks all operations needed to complete one planned resource.
type planAndApplyRow struct {
	change     tfexec.Change
	status     string
	started    time.Time
	completed  map[string]bool
	diagnostic string
}

// planAndApplyModel owns execution progress, review focus, and panel state.
type planAndApplyModel struct {
	opts                                                    PlanAndApplyOptions
	events                                                  <-chan tea.Msg
	cancel                                                  context.CancelFunc
	stage                                                   string
	started, ended                                          time.Time
	width, height, selected, focus, scroll                  int
	tableTop                                                int
	mouseX, mouseY                                          int
	rows                                                    map[string]*planAndApplyRow
	plan                                                    tfexec.Plan
	review                                                  *planAndApplyReview
	logs                                                    []string
	logBytes                                                int
	logsDiscarded                                           bool
	diagnostics                                             []string
	problem                                                 string
	detail, logView, outputView, inlineMode, stopping, done bool
	followLogs                                              bool
	hideCompleted                                           bool
	selection                                               *logSelection
	copyStatus                                              string
	err                                                     error
}

// Init starts the dashboard clock without taking ownership of engine events.
func (m planAndApplyModel) Init() tea.Cmd { return planAndApplyClock() }

// planAndApplyClock schedules one dashboard refresh.
func planAndApplyClock() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg { return planAndApplyTick(t) })
}

// Update handles input and consumes engine events on the model's own goroutine.
func (m planAndApplyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch e := msg.(type) {
	case planAndApplyCopied:
		m.copyStatus = "Copied"
		if e.err != nil {
			m.copyStatus = "Copy failed: " + e.err.Error()
		}
	case tea.WindowSizeMsg:
		m.width, m.height = e.Width, e.Height
		m.selection = nil
	case tea.MouseMsg:
		m.mouseX, m.mouseY = e.X, e.Y
		if m.logView && !m.small() {
			_, lines, n := m.modalContent()
			width := max(1, min(m.width-4, 116)-8)
			start := min(m.scroll, max(0, len(lines)-n))
			if m.followLogs {
				start = max(0, len(lines)-n)
			}
			inside := e.X >= 5 && e.X < 5+width && e.Y >= 5 && e.Y < 5+min(n, len(lines)-start)
			if e.Button == tea.MouseButtonLeft && e.Action == tea.MouseActionPress && inside {
				point := (start+e.Y-5)*width + e.X - 5
				m.selection = &logSelection{lines: lines, width: width, start: point, end: point, dragging: true}
				m.followLogs, m.scroll, m.copyStatus = false, start, ""
				return m, nil
			}
			if m.selection != nil && m.selection.dragging && (e.Action == tea.MouseActionMotion || e.Action == tea.MouseActionRelease) {
				row := max(start, min(start+min(n, len(lines)-start)-1, start+e.Y-5))
				m.selection.end = row*width + max(0, min(width-1, e.X-5))
				if e.Action == tea.MouseActionRelease {
					m.selection.dragging = false
					if m.selection.start != m.selection.end {
						return m, copyPlanAndApplyLogs(m.selection.text())
					}
					m.selection = nil
				}
				return m, nil
			}
		}
		if m.small() || e.Action != tea.MouseActionPress {
			break
		}
		if m.detail || m.logView || m.outputView {
			if e.X >= 3 && e.X < min(m.width-4, 116)-1 && e.Y >= 2 && e.Y < m.height-4 {
				switch e.Button {
				case tea.MouseButtonWheelUp:
					return m.Update(tea.KeyMsg{Type: tea.KeyUp})
				case tea.MouseButtonWheelDown:
					return m.Update(tea.KeyMsg{Type: tea.KeyDown})
				}
			} else if m.detail && e.Button == tea.MouseButtonLeft {
				m.detail, m.scroll = false, 0
			}
			break
		}
		// The table starts after the border, title, summary, and column header.
		const firstRow = 10
		if e.X < 2 || e.X >= min(m.width-4, 116) || e.Y < firstRow || e.Y >= firstRow+m.visibleRows() {
			break
		}
		switch e.Button {
		case tea.MouseButtonWheelUp:
			return m.Update(tea.KeyMsg{Type: tea.KeyUp})
		case tea.MouseButtonWheelDown:
			return m.Update(tea.KeyMsg{Type: tea.KeyDown})
		case tea.MouseButtonLeft:
			selected := m.resourceAt(e.X, e.Y)
			if selected >= 0 {
				m.selected, m.focus, m.scroll = selected, 0, 0
				m.detail = true
			}
		}
	case planAndApplyTick:
		// Consume only in Update: an outstanding blocking tea.Cmd could steal
		// an event when the user switches to Inline mode.
		for i := 0; i < 64 && !m.done; i++ {
			select {
			case event := <-m.events:
				m.accept(event)
				if m.review != nil && (m.opts.AutoApprove || !m.plan.HasChanges) {
					m.review.answer <- true
					m.review = nil
				}
			default:
				return m, planAndApplyClock()
			}
		}
		if !m.done {
			return m, planAndApplyClock()
		}
	case tea.KeyMsg:
		m.selection = nil
		m.copyStatus = ""
		switch e.String() {
		case "c":
			if m.logView {
				m.copyStatus = "Copying…"
				return m, copyPlanAndApplyLogs(strings.Join(m.logs, "\n"))
			}
		case "ctrl+c":
			if m.done {
				return m, tea.Quit
			}
			m.stopping = true
			m.cancel()
		case "ctrl+o":
			m.inlineMode = true
			return m, tea.Quit
		case "esc":
			m.detail, m.logView, m.outputView, m.scroll = false, false, false, 0
		case "l":
			m.logView, m.detail, m.outputView, m.scroll = true, false, false, 0
			m.followLogs = true
		case "o":
			m.outputView, m.detail, m.logView, m.scroll = true, false, false, 0
		case "h":
			if m.stage == "Apply" && !m.detail && !m.logView && !m.outputView {
				m.hideCompleted = !m.hideCompleted
			}
		case "home", "end", "g", "G":
			if m.detail || m.logView || m.outputView {
				_, lines, n := m.modalContent()
				bottom := e.String() == "end" || e.String() == "G"
				m.scroll = 0
				if bottom {
					m.scroll = max(0, len(lines)-n)
				}
				m.followLogs = m.logView && bottom
			}
		case "up", "down", "pgup", "pgdown", " ", "b", "ctrl+b", "ctrl+f":
			delta := 1
			if e.String() == "up" || e.String() == "pgup" || e.String() == "b" || e.String() == "ctrl+b" {
				delta = -1
			}
			page := e.String() != "up" && e.String() != "down"
			if m.detail || m.logView || m.outputView {
				_, lines, n := m.modalContent()
				last := max(0, len(lines)-n)
				if m.logView && m.followLogs {
					m.scroll = last
				}
				if page {
					delta *= n
				}
				m.scroll = max(0, min(last, m.scroll+delta))
				if delta < 0 {
					m.followLogs = false
				}
			} else {
				if page {
					delta *= m.visibleRows()
				}
				indices := m.tableIndices()
				if len(indices) > 0 {
					position := slices.Index(indices, m.selected)
					m.selected = indices[max(0, min(len(indices)-1, position+delta))]
				}
				m.focus = 0
			}
		case "tab", "shift+tab":
			if !m.detail && !m.logView && !m.outputView {
				direction := 1
				if e.String() == "shift+tab" {
					direction = -1
				}
				stops := 3
				if m.done {
					stops = 2
				}
				m.focus = (m.focus + direction + stops) % stops
			}
		case "enter":
			if m.small() || m.detail || m.logView || m.outputView {
				break
			}
			if m.focus == 0 && len(m.tableIndices()) > 0 {
				m.detail = true
				m.scroll = 0
			} else if m.done {
				return m, tea.Quit
			} else if m.review != nil && m.focus > 0 {
				m.review.answer <- m.focus == 2
				m.review = nil
				m.focus = 0
			}
		}
	case tfexec.Event, planAndApplyReview, planAndApplyDone:
		m.accept(msg)
		if m.review != nil && (m.opts.AutoApprove || !m.plan.HasChanges) {
			m.review.answer <- true
			m.review = nil
		}
	}
	m.adjustTable()
	return m, nil
}

// accept applies an execution event independently of the active renderer.
func (m *planAndApplyModel) accept(msg tea.Msg) {
	defer m.adjustTable()
	switch e := msg.(type) {
	case planAndApplyReview:
		m.plan, m.review, m.focus = e.plan, &e, 0
		m.selected, m.tableTop = 0, 0
		for _, c := range e.plan.Changes {
			m.rows[c.Address] = &planAndApplyRow{change: c, status: "Waiting", completed: map[string]bool{}}
		}
	case planAndApplyDone:
		m.done, m.err, m.ended = true, e.err, time.Now()
		m.review = nil
		if e.err != nil {
			m.addLog(e.err.Error())
		}
		for _, r := range m.rows {
			if r.status != "Complete" && r.status != "Failed" {
				r.status = "Not completed"
			}
		}
	case tfexec.Event:
		if e.Stage != "" {
			m.stage = e.Stage
		}
		if e.Message != "" {
			m.addLog(e.Message)
		}
		if e.Type == "diagnostic" {
			diagnostic := ansi.Strip(e.Message)
			if !slices.Contains(m.diagnostics, diagnostic) {
				m.diagnostics = append(m.diagnostics, diagnostic)
			}
			if strings.HasPrefix(e.Message, "error:") {
				m.problem, _, _ = strings.Cut(ansi.Strip(e.Message), "\n")
			}
		}
		if e.Type == "apply_errored" && m.problem == "" {
			m.problem = ansi.Strip(e.Message)
		}
		r := m.rows[e.Address]
		if r == nil {
			return
		}
		switch e.Type {
		case "apply_start":
			r.status = "Running"
			r.started = time.Now()
		case "apply_complete":
			r.completed[e.Action] = true
			complete := true
			for _, action := range r.change.Actions {
				complete = complete && r.completed[action]
			}
			if complete {
				r.status = "Complete"
			} else {
				r.status = "Waiting"
			}
		case "apply_errored":
			r.status = "Failed"
			r.diagnostic = e.Message
		case "diagnostic":
			r.diagnostic = e.Message
		}
	}
}

// Allow ample room for provider diagnostics and long-running apply heartbeats.
// Charge per-entry overhead too, so tiny messages cannot grow the slice unboundedly.
const planAndApplyLogBudget = 64 << 20
const planAndApplyLogOverhead = 32

// addLog retains unstyled output within the memory budget and preserves paused scrolling.
func (m *planAndApplyModel) addLog(message string) {
	message = ansi.Strip(message)
	if len(message)+planAndApplyLogOverhead > planAndApplyLogBudget {
		end := planAndApplyLogBudget - planAndApplyLogOverhead
		for !utf8.RuneStart(message[end]) {
			end--
		}
		message = strings.Clone(message[:end])
		m.logsDiscarded = true
	}
	m.logs = append(m.logs, message)
	m.logBytes += len(message) + planAndApplyLogOverhead
	for m.logBytes > planAndApplyLogBudget {
		if m.logView && !m.followLogs && m.selection == nil {
			width := max(1, min(m.width-4, 116)-8)
			m.scroll = max(0, m.scroll-len(strings.Split(ansi.Wrap(m.logs[0], width, ""), "\n")))
		}
		m.logBytes -= len(m.logs[0]) + planAndApplyLogOverhead
		m.logs[0] = ""
		m.logs = m.logs[1:]
		m.logsDiscarded = true
	}
}

// inlineReview prints the redacted plan and answers its pending approval request.
func (m *planAndApplyModel) inlineReview(ctx context.Context) {
	for _, c := range m.plan.Changes {
		fmt.Printf("%-8s %s\n", c.Action(), c.Address)
	}
	if m.plan.Outputs != "" {
		fmt.Println("Output changes:\n" + m.plan.Outputs)
	}
	if m.review == nil {
		return
	}
	ok := true
	if m.plan.HasChanges && !m.opts.AutoApprove {
		// Inline review includes redacted details before asking for approval.
		for _, c := range m.plan.Changes {
			fmt.Printf("\n%s\n%s\n", c.Address, c.Details)
		}
		var err error
		ok, err = planAndApplyApproval(ctx)
		if err != nil {
			ok = false
		}
	}
	m.review.answer <- ok
	m.review = nil
}

// planAndApplyApproval waits for an explicit yes without leaving a stdin-reading
// goroutine behind when Ctrl-C, SIGTERM, or a deadline cancels review.
func planAndApplyApproval(ctx context.Context) (bool, error) {
	fmt.Print("Apply this saved plan? Type yes to continue: ")
	line, err := ReadLine(ctx)
	return strings.EqualFold(strings.TrimSpace(line), "yes"), err
}

// ReadLine reads one answer without consuming the next prompt's input or leaving
// a reader behind when cancellation interrupts an Inline-mode workflow.
func ReadLine(ctx context.Context) (string, error) {
	fd := int(os.Stdin.Fd())
	var line strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var ready unix.FdSet
		ready.Set(fd)
		timeout := unix.NsecToTimeval(int64(100 * time.Millisecond))
		n, err := unix.Select(fd+1, &ready, nil, nil, &timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}
		var b [1]byte
		n, err = unix.Read(fd, b[:])
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", io.EOF
		}
		if b[0] == '\n' {
			return strings.TrimSpace(line.String()), nil
		}
		line.WriteByte(b[0])
	}
}

// counts summarizes every planned resource, including rows hidden by filtering.
func (m planAndApplyModel) counts() (complete, active, failed int) {
	for _, r := range m.rows {
		switch r.status {
		case "Complete":
			complete++
		case "Running":
			active++
		case "Failed":
			failed++
		}
	}
	return
}

// status describes the current phase, prioritizing cancellation and failures.
func (m planAndApplyModel) status() string {
	if m.done {
		if m.err != nil {
			if m.problem != "" && !m.stopping {
				return m.stage + " failed · " + m.problem
			}
			return m.stage + " stopped: " + m.err.Error()
		}
		if !m.plan.HasChanges {
			return "Infrastructure is up to date"
		}
		return "Infrastructure applied"
	}
	if m.stopping {
		return "Stopping safely; waiting for engine and state cleanup…"
	}
	if m.problem != "" {
		return "Error encountered; waiting for remaining operations and cleanup…"
	}
	if m.stage == "Review" {
		return "Plan ready · waiting for approval"
	}
	return m.stage + " in progress"
}

// small reports whether the terminal can display the dashboard's controls.
func (m planAndApplyModel) small() bool { return m.width < MinTUIWidth || m.height < MinTUIHeight }

// tableIndices preserves plan order and keeps selection tied to resource identity.
func (m planAndApplyModel) tableIndices() []int {
	indices := make([]int, 0, len(m.plan.Changes))
	for i, c := range m.plan.Changes {
		if m.stage == "Apply" && m.hideCompleted && m.rows[c.Address].status == "Complete" {
			continue
		}
		indices = append(indices, i)
	}
	return indices
}

// adjustTable keeps selection visible without moving it inside an open details panel.
func (m *planAndApplyModel) adjustTable() {
	indices := m.tableIndices()
	position, found := slices.BinarySearch(indices, m.selected)
	position = max(0, min(position, len(indices)-1))
	// An open details panel must not switch resources when its resource completes.
	if !found && len(indices) > 0 && !m.detail {
		m.selected = indices[position]
	}
	n := m.visibleRows()
	m.tableTop = max(position-n+1, min(m.tableTop, position))
	m.tableTop = max(0, min(m.tableTop, len(indices)-n))
}

// resourceAt hits only the displayed name, excluding row padding and other columns.
func (m planAndApplyModel) resourceAt(x, y int) int {
	if m.small() || m.detail || m.logView || m.outputView || y < 10 || y >= 10+m.visibleRows() {
		return -1
	}
	i := m.tableTop + y - 10
	indices := m.tableIndices()
	if i >= len(indices) {
		return -1
	}
	i = indices[i]
	left, width := 4, min(m.width-4, 116)-12
	if m.stage == "Apply" {
		left, width = 5, min(m.width-4, 116)-31
	}
	name := ansi.Truncate(m.plan.Changes[i].Address, width, "…")
	if x < left || x >= left+ansi.StringWidth(name) {
		return -1
	}
	return i
}

// visibleRows reserves space for the summary, activity panel, and controls.
func (m planAndApplyModel) visibleRows() int {
	return max(1, m.height-17-lipgloss.Height(m.activity()))
}

// activity renders the latest message in a fixed-height panel to avoid table jumps.
func (m planAndApplyModel) activity() string {
	latest := ""
	if len(m.logs) > 0 {
		latest = m.logs[len(m.logs)-1]
	}
	if m.done && m.err != nil && m.stage == "Apply" {
		latest = "Applied changes were not rolled back. Press l for recovery diagnostics."
	}
	width := max(1, min(m.width-4, 116)-4)
	lines := strings.Split(ansi.Wrap(latest, width, ""), "\n")
	if len(lines) > 3 {
		lines = append(lines[:2], "… Press l for full logs")
	}
	return lipgloss.NewStyle().Width(width+2).Height(3).Padding(0, 1).
		Background(lipgloss.AdaptiveColor{Light: "#EEEADF", Dark: "#252A32"}).
		Render(strings.Join(lines, "\n"))
}

// detailSummary renders resource identity and operation above the scrollable diff.
func (m planAndApplyModel) detailSummary() string {
	if !m.detail || len(m.plan.Changes) == 0 {
		return ""
	}
	r := m.rows[m.plan.Changes[m.selected].Address]
	width := max(1, min(m.width-4, 116)-8)
	// Bound the fixed header so long addresses still leave room for the diff.
	name := ansi.Wrap(ansi.Truncate(r.change.Address, width*2, "…"), width, "")
	return name + "\nStatus: " + r.status + "\n" +
		lipgloss.NewStyle().Bold(true).Render("Operation:") + " " + strings.Join(r.change.Actions, " → ") +
		"\n" + strings.Repeat("─", width)
}

// modalContent returns the active panel's title, wrapped lines, and viewport height.
func (m planAndApplyModel) modalContent() (string, []string, int) {
	width := max(1, min(m.width-4, 116)-8)
	title, text := "Engine logs", strings.Join(m.logs, "\n")
	if m.outputView {
		title, text = "Output changes", m.plan.Outputs
		header := lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary).
			Background(lipgloss.AdaptiveColor{Light: "#EEEADF", Dark: "#252A32"}).Width(width)
		bold := lipgloss.NewStyle().Bold(true)
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		for i, line := range lines {
			if line != "" && !strings.HasPrefix(line, " ") {
				lines[i] = header.Render(ansi.Wrap(line, width, ""))
			} else if strings.HasPrefix(line, "  Before:") {
				lines[i] = "  " + bold.Render("Before:") + strings.TrimPrefix(line, "  Before:")
			} else if strings.HasPrefix(line, "  After:") {
				lines[i] = "  " + bold.Render("After:") + strings.TrimPrefix(line, "  After:")
			}
		}
		text = strings.Join(lines, "\n")
	}
	if m.detail && len(m.plan.Changes) > 0 {
		r := m.rows[m.plan.Changes[m.selected].Address]
		bold := lipgloss.NewStyle().Bold(true)
		details := strings.NewReplacer(
			"\n\nBefore\n", "\n\n"+bold.Render("Before")+"\n",
			"\n\nAfter\n", "\n\n"+bold.Render("After")+"\n",
		).Replace(r.change.Details)
		if strings.HasPrefix(details, "Operation:") {
			_, details, _ = strings.Cut(details, "\n\n")
		}
		title, text = "Resource details", details
		if r.diagnostic != "" {
			text += "\n\nDiagnostic\n" + r.diagnostic
		}
	}
	n := max(1, m.height-14)
	if m.logView && m.selection != nil {
		return title, m.selection.lines, n
	}
	if summary := m.detailSummary(); summary != "" {
		n = max(1, n-lipgloss.Height(summary)-1)
	}
	return title, strings.Split(ansi.Wrap(text, width, ""), "\n"), n
}

// View renders the dashboard, active panel, or small-terminal escape route.
func (m planAndApplyModel) View() string {
	statusStyle := lipgloss.NewStyle().Bold(true)
	if m.small() {
		return fmt.Sprintf("%s\n\n%s\n\nThis view needs at least 80 columns × 24 rows.\nCurrent terminal: %d × %d.\n\nResize to restore the dashboard.\nPress Ctrl+O to continue in Inline mode.\nCtrl-C requests a safe stop.\n", m.opts.Title, statusStyle.Render(m.status()), m.width, m.height)
	}
	w := min(m.width-4, 116)
	primary := lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)
	card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5865f2")).Padding(0, 1).Width(w)
	if m.detail || m.logView || m.outputView {
		title, lines, n := m.modalContent()
		start := min(m.scroll, max(0, len(lines)-n))
		if m.logView && m.followLogs {
			start = max(0, len(lines)-n)
		}
		end := min(len(lines), start+n)
		body := primary.Render(title) + "\n\n"
		if summary := m.detailSummary(); summary != "" {
			body += summary + "\n\n"
		}
		visible := append([]string(nil), lines[start:end]...)
		if m.logView && m.selection != nil {
			s := m.selection
			first, last := min(s.start, s.end), max(s.start, s.end)
			for i, line := range visible {
				row := start + i
				if row < first/s.width || row > last/s.width {
					continue
				}
				from, to := 0, ansi.StringWidth(line)
				if row == first/s.width {
					from = first % s.width
				}
				if row == last/s.width {
					to = min(to, last%s.width+1)
				}
				to = max(from, to)
				highlight := lipgloss.NewStyle().Background(ColorSecondary).Foreground(lipgloss.Color("#102526"))
				visible[i] = ansi.Cut(line, 0, from) + highlight.Render(ansi.Cut(line, from, to)) + ansi.Cut(line, to, ansi.StringWidth(line))
			}
		}
		body += lipgloss.NewStyle().Height(n).Render(strings.Join(visible, "\n"))
		info := fmt.Sprintf("%d–%d/%d", start+1, end, len(lines))
		if m.logView {
			if m.followLogs {
				info += " · Following"
			} else {
				info += " · Paused"
			}
			info += " · c Copy logs"
			if m.copyStatus != "" {
				info = m.copyStatus
			}
			if m.logsDiscarded {
				info = "Log limit reached · " + info
			}
		}
		body += "\n\n" + ansi.Truncate(info, w-8, "…")
		body += "\n↑↓ Line · Ctrl+B/PgUp Up · Ctrl+F/PgDn/Space Down\nHome/g Top · End/G Bottom · Esc Close · Ctrl+O Inline mode"
		background := m
		background.detail, background.logView, background.outputView = false, false, false
		return planAndApplyOverlay(background.View(), card.Width(w-6).Render(body), 3, 2)
	}
	card = card.Height(m.height - 3)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", primary.Render(m.opts.Title), ansi.Truncate(m.opts.Subtitle, w-2, "…"))
	status := m.status()
	warnings := 0
	for _, diagnostic := range m.diagnostics {
		if strings.HasPrefix(diagnostic, "warning:") {
			warnings++
		}
	}
	if warnings > 0 {
		status += fmt.Sprintf(" · %d warnings (l Logs)", warnings)
	}
	fmt.Fprintln(&b, statusStyle.Render(ansi.Truncate(status, w-2, "…")))
	if m.stage != "Review" {
		elapsed := time.Since(m.started)
		if m.done {
			elapsed = m.ended.Sub(m.started)
		}
		fmt.Fprintf(&b, "Elapsed %s\n", elapsed.Round(time.Second))
	} else {
		b.WriteString("\n")
	}
	if len(m.plan.Changes) == 0 && (m.stage == "Init" || m.stage == "Plan") && !m.done {
		if len(m.logs) > 0 {
			latest := strings.ReplaceAll(m.logs[len(m.logs)-1], "\n", " ")
			b.WriteString("\n" + ansi.Truncate(latest, w-2, "…"))
		}
		body := lipgloss.NewStyle().Height(m.height - 5).Render(strings.TrimRight(b.String(), "\n"))
		body += "\n\nl Logs  Ctrl+O Inline mode  Ctrl-C Stop"
		return card.Render(body) + "\n" + m.stages()
	}
	complete, active, failed := m.counts()
	if m.stage == "Apply" && len(m.plan.Changes) > 0 {
		percent := complete * 100 / len(m.plan.Changes)
		fmt.Fprintf(&b, "%s %d%% · %d/%d complete\n%d active · %d failed\n", renderBar(int64(complete), int64(len(m.plan.Changes)), 24), percent, complete, len(m.plan.Changes), active, failed)
	} else {
		counts := map[string]int{}
		for _, c := range m.plan.Changes {
			counts[c.Action()]++
		}
		fmt.Fprintf(&b, "%d create · %d update · %d replace · %d delete · %d read\n", counts["Create"], counts["Update"], counts["Replace"], counts["Delete"], counts["Read"])
		if counts["Replace"]+counts["Delete"] > 0 {
			b.WriteString("! Plan includes destruction of existing resources.\n")
		} else {
			b.WriteString("\n")
		}
	}
	addressWidth := w - 31
	header := ""
	if m.stage == "Apply" {
		header = fmt.Sprintf("   %-*s %-8s %s", addressWidth, "Resource", "Action", "Status")
	} else {
		addressWidth = w - 12
		header = fmt.Sprintf("  %-*s %-8s", addressWidth, "Resource", "Action")
	}
	muted := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#687477", Dark: "#9BA5AD"})
	b.WriteString(statusStyle.Render(header) + "\n" + muted.Render(strings.Repeat("─", w-2)) + "\n")
	n := m.visibleRows()
	start := m.tableTop
	indices := m.tableIndices()
	end := min(len(indices), start+n)
	hovered := m.resourceAt(m.mouseX, m.mouseY)
	for position := start; position < end; position++ {
		i := indices[position]
		c := m.plan.Changes[i]
		r := m.rows[c.Address]
		marker := "○"
		switch r.status {
		case "Running":
			marker = "◉"
		case "Complete":
			marker = "✓"
		case "Failed":
			marker = "✕"
		}
		status := r.status
		if status == "Running" {
			status = time.Since(r.started).Round(time.Second).String()
		}
		cursor := " "
		if i == m.selected && m.focus == 0 {
			cursor = ">"
		}
		name := ansi.Truncate(c.Address, addressWidth, "…")
		padding := strings.Repeat(" ", max(0, addressWidth-ansi.StringWidth(name)))
		if i == hovered {
			name = lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Underline(true).Render(name)
		}
		line := fmt.Sprintf("%s%s %s%s %-8s %s", cursor, marker, name, padding, c.Action(), status)
		if m.stage != "Apply" {
			line = fmt.Sprintf("%s %s%s %-8s", cursor, name, padding, c.Action())
		}
		rowStyle := lipgloss.NewStyle().Width(w - 2)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(lipgloss.AdaptiveColor{Light: "#EEEADF", Dark: "#252A32"})
		}
		if i == m.selected && m.focus == 0 {
			rowStyle = rowStyle.Foreground(ColorSecondary).Bold(true).
				Background(lipgloss.AdaptiveColor{Light: "#DCEEEA", Dark: "#153D43"})
		}
		b.WriteString(rowStyle.Render(line) + "\n")
	}
	for i := end - start; i < n; i++ {
		if i == 0 && m.hideCompleted && m.stage == "Apply" {
			b.WriteString("All resources applied! Press h to show completed resources.\n")
		} else {
			b.WriteString("\n")
		}
	}
	rowCount := fmt.Sprintf("Rows %d–%d of %d", min(start+1, len(indices)), end, len(indices))
	if m.stage == "Apply" {
		if m.hideCompleted {
			rowCount += " · h Show completed"
		} else {
			rowCount += " · h Hide completed"
		}
	}
	fmt.Fprintln(&b, muted.Render(rowCount))
	b.WriteString(m.activity() + "\n\n")
	if m.review != nil {
		cancel, approve := "[ Cancel ]", "[ Approve and apply ]"
		if m.focus == 1 {
			cancel = primary.Render("> " + cancel)
		}
		if m.focus == 2 {
			approve = primary.Render("> " + approve)
		}
		b.WriteString(cancel + "    " + approve + "\n")
	} else if m.done {
		close := "[ Close ]"
		if m.focus != 0 {
			close = primary.Render("> " + close)
		}
		b.WriteString(close + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("↑↓ Select  Enter Details  Tab Actions  l Logs  o Outputs\nCtrl+O Inline mode  Ctrl-C Stop  Ctrl+B/F/PgUp/PgDn Page  Space Next")
	return card.Render(b.String()) + "\n" + m.stages()
}

// planAndApplyOverlay places a modal over the dimmed dashboard without changing
// its selection or the resource updates continuing underneath it.
func planAndApplyOverlay(background, modal string, x, y int) string {
	lines := strings.Split(background, "\n")
	for len(lines) < y+lipgloss.Height(modal) {
		lines = append(lines, "")
	}
	dim := lipgloss.NewStyle().Faint(true)
	for i, line := range lines {
		lines[i] = dim.Render(ansi.Strip(line))
	}
	for i, line := range strings.Split(modal, "\n") {
		row := y + i
		prefix := lipgloss.NewStyle().Width(x).Render(ansi.Cut(lines[row], 0, x))
		lines[row] = prefix + line + ansi.Cut(lines[row], x+ansi.StringWidth(line), ansi.StringWidth(lines[row]))
	}
	return strings.Join(lines, "\n")
}

// stages renders wizard progress without implying that skipped apply work ran.
func (m planAndApplyModel) stages() string {
	stages := []string{"Questions", "Init", "Plan", "Review", "Apply"}
	current := 1
	for i, s := range stages {
		if s == m.stage {
			current = i
		}
	}
	for i, s := range stages {
		icon := "○"
		if i < current {
			icon = "✓"
		}
		if i == current {
			icon = "◉"
			if m.done {
				if m.err != nil {
					icon = "✕"
				} else {
					icon = "✓"
				}
			}
		}
		if m.done && m.err == nil && !m.plan.HasChanges && s == "Apply" {
			icon, s = "–", "Apply not needed"
		}
		stages[i] = icon + " " + s
	}
	return " " + strings.Join(stages, " → ")
}
