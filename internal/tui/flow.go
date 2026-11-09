// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// flowKey identifies the terminal session shared by a create workflow.
type flowKey struct{}

// flowSession connects sequential workflow operations to one terminal owner.
type flowSession struct {
	messages   chan tea.Msg
	inlineMode atomic.Bool
	released   chan struct{}
}

// flowPrompt hands a model to the terminal until its Quit command completes it.
type flowPrompt struct {
	model  tea.Model
	result chan tea.Model
}

// flowOutput records preparation messages without writing over the renderer.
type flowOutput string

// flowSummary retains a result or warning after leaving the alternate screen.
type flowSummary string

// flowHeading changes the current workflow's title and stage.
type flowHeading struct{ title, stage string }

// flowFinished closes the session only after its operations have returned.
type flowFinished struct{ err error }

// flowResume identifies the saved configuration to use after an interruption.
type flowResume string

// flowCancelPrompt relinquishes a prompt before its caller cleans up its state.
type flowCancelPrompt struct{ result chan tea.Model }

// flowCommand tags asynchronous results so an old prompt cannot update a new one.
type flowCommand struct {
	generation int
	msg        tea.Msg
}

// flowModel hosts prompts and dashboards without owning their business logic.
type flowModel struct {
	session        *flowSession
	cancel         context.CancelFunc
	title, stage   string
	width, height  int
	model          tea.Model
	result         chan tea.Model
	generation     int
	logs           []string
	summary        []string
	inlineMode     bool
	resume         string
	failed         bool
	promptCanceled bool
	logPanel       *planAndApplyModel
}

// RunFlow owns one alternate-screen session for a complete create workflow.
// Nested flows reuse the session; non-interactive callers run the same operations.
func RunFlow(ctx context.Context, title string, run func(context.Context) error) error {
	if _, ok := ctx.Value(flowKey{}).(*flowSession); ok {
		FlowStage(ctx, title, "Questions")
		return run(ctx)
	}
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if !CanUseTUI(0, 0).OK || !term.IsTerminal(int(os.Stdin.Fd())) {
		return run(ctx)
	}
	s := &flowSession{messages: make(chan tea.Msg, 64), released: make(chan struct{})}
	ctx = context.WithValue(ctx, flowKey{}, s)
	finished := make(chan error, 1)
	go func() {
		result := run(ctx)
		finished <- result
		s.messages <- flowFinished{result}
	}()
	m := flowModel{session: s, cancel: cancel, title: title, stage: "Questions"}
	final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithoutSignalHandler()).Run()
	s.inlineMode.Store(true)
	if err != nil {
		cancel()
	} else {
		m = final.(flowModel)
		if m.inlineMode {
			for _, line := range m.logs {
				fmt.Println(line)
			}
		}
		for _, line := range m.summary {
			fmt.Println(line)
		}
		if m.failed && m.resume != "" {
			fmt.Println("Resume: " + m.resume)
		}
	}
	close(s.released)
	// Keep draining during shutdown: engine cleanup must finish before returning.
	for {
		select {
		case result := <-finished:
			if err != nil {
				return err
			}
			return result
		case message := <-s.messages:
			if request, ok := message.(flowPrompt); ok {
				request.result <- nil
			}
			if summary, ok := message.(flowSummary); ok {
				fmt.Println(summary)
			}
		}
	}
}

// Run presents a component inside the active flow or on its own.
// RunInline implementations continue from the component's current answers.
func Run(ctx context.Context, model tea.Model) (tea.Model, error) {
	if s, ok := ctx.Value(flowKey{}).(*flowSession); ok && !s.inlineMode.Load() {
		request := flowPrompt{model, make(chan tea.Model, 1)}
		select {
		case s.messages <- request:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		cancelled := ctx.Done()
		for {
			select {
			case model = <-request.result:
				if model == nil {
					return nil, context.Canceled
				}
				if s.inlineMode.Load() {
					<-s.released
					if dashboard, ok := model.(planAndApplyModel); ok {
						return dashboard, nil
					}
					return runInline(ctx, model)
				}
				return model, nil
			case <-cancelled:
				s.messages <- flowCancelPrompt{request.result}
				cancelled = nil
			case <-s.released:
				// The renderer failed before replying; the caller must stop work.
				if !s.inlineMode.Load() {
					return nil, context.Canceled
				}
				select {
				case model = <-request.result:
				default:
					return nil, context.Canceled
				}
				if model == nil {
					return nil, context.Canceled
				}
				if dashboard, ok := model.(planAndApplyModel); ok {
					return dashboard, nil
				}
				return runInline(ctx, model)
			}
		}
	}
	if !CanUseTUI(0, 0).OK || flowInline(ctx) {
		return runInline(ctx, model)
	}
	if _, ok := model.(planAndApplyModel); ok {
		return tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithoutSignalHandler()).Run()
	}
	return tea.NewProgram(model, tea.WithContext(ctx)).Run()
}

// runInline continues a component with scrolling output and prompts.
func runInline(ctx context.Context, model tea.Model) (tea.Model, error) {
	if inline, ok := model.(interface {
		RunInline(context.Context) (tea.Model, error)
	}); ok {
		return inline.RunInline(ctx)
	}
	return nil, fmt.Errorf("component %T does not support Inline mode", model)
}

// flowInline reports an explicit transition out of the TUI flow.
func flowInline(ctx context.Context) bool {
	s, ok := ctx.Value(flowKey{}).(*flowSession)
	return ok && s.inlineMode.Load()
}

// FlowOutput routes preparation output into the active flow's progress history.
func FlowOutput(ctx context.Context) io.Writer {
	if s, ok := ctx.Value(flowKey{}).(*flowSession); ok {
		return s
	}
	return os.Stdout
}

// FlowSummary preserves important messages once, after the interactive session.
func FlowSummary(ctx context.Context, message string) {
	if message == "" {
		return
	}
	if s, ok := ctx.Value(flowKey{}).(*flowSession); ok {
		if !s.inlineMode.Load() {
			s.messages <- flowSummary(ansi.Strip(message))
			return
		}
		<-s.released
	}
	fmt.Println(message)
}

// FlowResume keeps the saved-config command available on failure or cancellation.
func FlowResume(ctx context.Context, command string) {
	if s, ok := ctx.Value(flowKey{}).(*flowSession); ok && !s.inlineMode.Load() {
		s.messages <- flowResume(command)
	}
}

// Write records complete progress messages or writes directly in Inline mode.
func (s *flowSession) Write(p []byte) (int, error) {
	if s.inlineMode.Load() {
		<-s.released
		return os.Stdout.Write(p)
	}
	s.messages <- flowOutput(strings.TrimSpace(ansi.Strip(string(p))))
	return len(p), nil
}

// FlowStage updates preparation progress, including returning from a nested flow.
func FlowStage(ctx context.Context, title, stage string) {
	if s, ok := ctx.Value(flowKey{}).(*flowSession); ok && !s.inlineMode.Load() {
		s.messages <- flowHeading{title, stage}
	}
}

// Init waits for the first workflow operation.
func (m flowModel) Init() tea.Cmd { return m.next() }

// next receives one workflow message without polling.
func (m flowModel) next() tea.Cmd {
	return func() tea.Msg { return <-m.session.messages }
}

// command scopes child commands, including batches, to the active prompt.
func (m flowModel) command(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	id := m.generation
	return func() tea.Msg { return flowCommand{id, cmd()} }
}

// Update routes input to the current component and keeps its Quit local.
func (m flowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case flowFinished:
		m.failed = v.err != nil
		return m, tea.Quit
	case flowResume:
		m.resume = string(v)
		return m, m.next()
	case flowCancelPrompt:
		if m.result == v.result {
			if _, ok := m.model.(planAndApplyModel); ok {
				m.promptCanceled = true
			} else {
				m.model, m.result = nil, nil
				v.result <- nil
			}
		}
		return m, m.next()
	case flowHeading:
		m.title, m.stage = v.title, v.stage
		return m, m.next()
	case flowOutput:
		if v != "" {
			m.logs = append(m.logs, string(v))
			if strings.HasPrefix(strings.ToLower(string(v)), "warning:") && !slices.Contains(m.summary, string(v)) {
				m.summary = append(m.summary, string(v))
			}
		}
		return m, m.next()
	case flowSummary:
		if !slices.Contains(m.summary, string(v)) {
			m.summary = append(m.summary, string(v))
			m.logs = append(m.logs, string(v))
		}
		return m, m.next()
	case flowPrompt:
		m.generation++
		m.promptCanceled = false
		m.logPanel = nil
		m.model, m.result = v.model, v.result
		size := tea.WindowSizeMsg{Width: m.width - 6, Height: m.height - 6}
		if dashboard, ok := m.model.(planAndApplyModel); ok {
			for _, line := range m.logs {
				dashboard.addLog(line)
			}
			m.model = dashboard
			size = tea.WindowSizeMsg{Width: m.width, Height: m.height}
		}
		var cmd tea.Cmd
		m.model, cmd = m.model.Update(size)
		return m, tea.Batch(m.next(), m.command(m.model.Init()), m.command(cmd))
	case flowCommand:
		if v.generation != m.generation || (m.model == nil && m.logPanel == nil) {
			return m, nil
		}
		if batch, ok := v.msg.(tea.BatchMsg); ok {
			cmds := make([]tea.Cmd, 0, len(batch))
			for _, cmd := range batch {
				cmds = append(cmds, m.command(cmd))
			}
			return m, tea.Batch(cmds...)
		}
		if _, ok := v.msg.(tea.QuitMsg); ok {
			if dashboard, ok := m.model.(planAndApplyModel); ok && dashboard.inlineMode {
				m.inlineMode = true
				m.session.inlineMode.Store(true)
			}
			m.result <- m.model
			m.model, m.result = nil, nil
			if m.inlineMode {
				return m, tea.Quit
			}
			return m, nil
		}
		msg = v.msg
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		if _, ok := m.model.(planAndApplyModel); !ok {
			msg = tea.WindowSizeMsg{Width: max(1, v.Width-6), Height: max(1, v.Height-6)}
		}
	case tea.KeyMsg:
		_, dashboard := m.model.(planAndApplyModel)
		if !dashboard && (v.String() == "ctrl+l" || (m.model == nil && v.String() == "l")) {
			if m.logPanel != nil {
				m.logPanel = nil
			} else {
				m.logPanel = &planAndApplyModel{opts: PlanAndApplyOptions{Title: m.title}, stage: m.stage, started: time.Now(), logView: true, followLogs: true}
			}
			return m, nil
		}
		if m.logPanel != nil && v.String() == "esc" {
			m.logPanel = nil
			return m, nil
		}
		if !dashboard && v.String() == "ctrl+o" {
			m.inlineMode = true
			m.session.inlineMode.Store(true)
			if m.result != nil {
				m.result <- m.model
			}
			return m, tea.Quit
		}
		if v.String() == "ctrl+c" && !dashboard {
			m.cancel()
			return m, nil
		}
		if !dashboard && (m.width < 80 || m.height < 24) {
			return m, nil
		}
	}
	if m.logPanel != nil {
		switch msg.(type) {
		case tea.KeyMsg, tea.MouseMsg, planAndApplyCopied:
			m.logPanel.width, m.logPanel.height, m.logPanel.logs = m.width, m.height, m.logs
			updated, cmd := m.logPanel.Update(msg)
			panel := updated.(planAndApplyModel)
			m.logPanel = &panel
			return m, m.command(cmd)
		}
	}
	if m.model != nil {
		var cmd tea.Cmd
		m.model, cmd = m.model.Update(msg)
		if dashboard, ok := m.model.(planAndApplyModel); ok && m.promptCanceled && dashboard.done {
			cmd = tea.Quit
		}
		return m, m.command(cmd)
	}
	return m, nil
}

// View uses the existing dashboard unchanged and frames configuration prompts.
func (m flowModel) View() string {
	if m.logPanel != nil {
		panel := *m.logPanel
		panel.width, panel.height, panel.logs = m.width, m.height, m.logs
		return panel.View()
	}
	if dashboard, ok := m.model.(planAndApplyModel); ok {
		return dashboard.View()
	}
	if m.width < 80 || m.height < 24 {
		return fmt.Sprintf("%s\n\nThis view needs at least 80 columns × 24 rows.\nResize, press Ctrl+O for Inline mode, or Ctrl-C to cancel.\n", m.title)
	}
	w := min(m.width-4, 116)
	primary := lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)
	body := primary.Render(m.title) + "\n\n"
	if m.model != nil {
		body += strings.TrimSpace(m.model.View())
	} else {
		body += lipgloss.NewStyle().Bold(true).Render(m.stage) + "\n\n"
		start := max(0, len(m.logs)-5)
		body += strings.Join(m.logs[start:], "\n")
	}
	body = ansi.Wrap(body, w-4, "")
	body = lipgloss.NewStyle().Height(m.height-5).Render(body) + "\n\nCtrl-L Logs · Ctrl+O Inline mode · Ctrl-C Cancel"
	card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5865f2")).Padding(0, 1).Width(w).Height(m.height - 3)
	return card.Render(body) + "\n" + (planAndApplyModel{stage: m.stage}).stages()
}
