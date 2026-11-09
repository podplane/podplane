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
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestFlowFrameHeight keeps the frame and stage footer fixed across workflow steps.
func TestFlowFrameHeight(t *testing.T) {
	for _, height := range []int{24, 40, 60} {
		flow := flowModel{width: 80, height: height, title: "Create cluster", stage: "Questions"}
		for _, stage := range []string{"Questions", "Init", "Plan", "Review", "Apply"} {
			t.Run(fmt.Sprintf("%s/%d", stage, height), func(t *testing.T) {
				flow.stage = stage
				switch stage {
				case "Questions":
					flow.model = confirmModel{message: "Continue?", width: 74}
				case "Init":
					flow.model = nil
					flow.logs = []string{"Preparing files…"}
				default:
					dashboard := planAndApplyFixture()
					dashboard.height = height
					dashboard.stage = stage
					if stage == "Plan" {
						dashboard.plan.Changes = nil
					}
					flow.model = dashboard
				}
				view := flow.View()
				lines := strings.Split(ansi.Strip(view), "\n")
				if lipgloss.Height(view) != height || !strings.HasPrefix(lines[height-2], "╰") || !strings.Contains(lines[height-1], "Questions") {
					t.Fatalf("frame or footer moved at height %d:\n%s", height, view)
				}
			})
		}
	}
}

// TestFlowPrompts keeps completion local and rejects late commands from old prompts.
func TestFlowPrompts(t *testing.T) {
	s := &flowSession{messages: make(chan tea.Msg, 16), released: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), flowKey{}, s), 3*time.Second)
	defer cancel()
	m := flowModel{session: s, cancel: cancel, width: 80, height: 24, title: "Create", stage: "Questions"}
	answer := make(chan string, 1)
	go func() {
		value, err := Input(ctx, "First", "Name", "first", Required("Name"))
		if err != nil {
			answer <- err.Error()
			return
		}
		value, err = Input(ctx, "Second", "Name", value+"-second", Required("Name"))
		if err != nil {
			answer <- err.Error()
			return
		}
		answer <- value
	}()
	update := func(msg tea.Msg) tea.Cmd {
		t.Helper()
		next, cmd := m.Update(msg)
		m = next.(flowModel)
		return cmd
	}
	select {
	case msg := <-s.messages:
		update(msg)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	quit := update(tea.KeyMsg{Type: tea.KeyEnter})()
	update(quit)
	select {
	case msg := <-s.messages:
		update(msg)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	update(quit)
	if !strings.Contains(m.View(), "first-second") {
		t.Fatal("late completion dismissed the second prompt")
	}
	update(update(tea.KeyMsg{Type: tea.KeyEnter})())
	select {
	case value := <-answer:
		if value != "first-second" {
			t.Fatalf("answers lost: %s", value)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// TestFlowSmallCancellation relinquishes the prompt before cancellation returns.
func TestFlowSmallCancellation(t *testing.T) {
	s := &flowSession{messages: make(chan tea.Msg, 16), released: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), flowKey{}, s))
	defer cancel()
	m := flowModel{session: s, cancel: cancel, width: 50, height: 12, title: "Create"}
	result := make(chan error, 1)
	go func() { _, err := Confirm(ctx, "Continue?", false, 0); result <- err }()
	update := func(msg tea.Msg) { next, _ := m.Update(msg); m = next.(flowModel) }
	select {
	case msg := <-s.messages:
		update(msg)
	case <-time.After(time.Second):
		t.Fatal("no prompt")
	}
	if !strings.Contains(m.View(), "Ctrl-C") {
		t.Fatal("small terminal has no cancellation hint")
	}
	update(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case msg := <-s.messages:
		update(msg)
	case <-time.After(time.Second):
		t.Fatal("cancellation stuck")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt did not cancel")
	}
}

// TestFlowInlineHandoff retains typed answers and does not rerun completed operations.
func TestFlowInlineHandoff(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = stdin; _ = reader.Close(); _ = writer.Close() })
	if _, err := io.WriteString(writer, "\nyes\n"); err != nil {
		t.Fatal(err)
	}
	s := &flowSession{messages: make(chan tea.Msg, 16), released: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), flowKey{}, s), 3*time.Second)
	defer cancel()
	m := flowModel{session: s, cancel: cancel, width: 80, height: 24, title: "Create"}
	result := make(chan error, 1)
	go func() {
		value, err := Input(ctx, "Name", "Name", "saved", Required("name"))
		if err != nil {
			result <- err
			return
		}
		if value != "saved-edited" {
			result <- fmt.Errorf("lost answer %q", value)
			return
		}
		ok, err := Confirm(ctx, "Continue?", false, 0)
		if err == nil && !ok {
			err = fmt.Errorf("next prompt consumed wrong answer")
		}
		result <- err
	}()
	update := func(msg tea.Msg) { next, _ := m.Update(msg); m = next.(flowModel) }
	select {
	case msg := <-s.messages:
		update(msg)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-edited")})
	update(tea.KeyMsg{Type: tea.KeyCtrlO})
	close(s.released)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
