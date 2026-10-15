// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestTaskProgressSmallTerminalShowsResizePrompt checks size guidance and the plain-output escape hint.
func TestTaskProgressSmallTerminalShowsResizePrompt(t *testing.T) {
	m := taskProgressModel{width: MinTUIWidth - 1, height: MinTUIHeight, tui: true}

	view := m.View()
	if !strings.Contains(view, "Podplane local start is still running.") {
		t.Fatalf("small terminal view missing running message:\n%s", view)
	}
	if !strings.Contains(view, "The installer view needs at least 80x24.") {
		t.Fatalf("small terminal view missing minimum size message:\n%s", view)
	}
	if !strings.Contains(view, "Press p to disable the TUI and continue with plain output.") {
		t.Fatalf("small terminal view missing plain-output hint:\n%s", view)
	}
}

// TestTaskProgressSmallTerminalPDisablesTUI checks switching to plain output in an undersized terminal.
func TestTaskProgressSmallTerminalPDisablesTUI(t *testing.T) {
	m := taskProgressModel{width: MinTUIWidth - 1, height: MinTUIHeight, tui: true}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updatedModel, ok := updated.(taskProgressModel)
	if !ok {
		t.Fatalf("Update returned %T, want taskProgressModel", updated)
	}
	if updatedModel.tui {
		t.Fatal("pressing p in a small terminal should disable TUI")
	}
}

// TestTaskProgressDoneViewShowsFrozenClusterStartDuration checks readiness bullets and a fixed completion duration.
func TestTaskProgressDoneViewShowsFrozenClusterStartDuration(t *testing.T) {
	now := time.Now()
	m := testTaskProgressModel([]TaskProgressItem{
		{Key: "work", Name: "work", Expected: time.Minute},
		{Key: "kubectl", Name: "kubectl works with context \"local\"", Ready: true},
	})
	m.doneReceived = true
	m.closed = true
	m.doneTitle = "✓ Local Podplane cluster \"default\" is ready"
	m.rows["work"] = taskProgressRow{
		item:      m.items[0],
		status:    "done",
		seen:      true,
		startedAt: now.Add(-5 * time.Minute),
		doneAt:    now.Add(-2*time.Minute - 40*time.Second),
	}
	m.rows["kubectl"] = taskProgressRow{
		item:      m.items[1],
		status:    "done",
		seen:      true,
		startedAt: now.Add(-2*time.Minute - 40*time.Second),
		doneAt:    now.Add(-2*time.Minute - 40*time.Second),
	}

	view := m.View()

	if !strings.Contains(view, "• kubectl works with context \"local\"") {
		t.Fatalf("done view missing ready bullet:\n%s", view)
	}
	if !strings.Contains(view, "Cluster started in 2m20s") {
		t.Fatalf("done view missing frozen cluster start duration:\n%s", view)
	}
}

// TestTaskProgressRemainingCountsDownFromFullPlan checks elapsed progress against all planned task durations.
func TestTaskProgressRemainingCountsDownFromFullPlan(t *testing.T) {
	m := testTaskProgressModel([]TaskProgressItem{
		{Key: "first", Name: "first", Expected: 30 * time.Second},
		{Key: "second", Name: "second", Expected: 70 * time.Second},
	})
	now := time.Now()
	m.rows["first"] = taskProgressRow{item: m.items[0], status: "running", seen: true, startedAt: now.Add(-10 * time.Second)}

	current, total, complete, tracked, remaining := m.overallProgress()

	if total != 100*time.Second {
		t.Fatalf("total = %s, want 1m40s", total)
	}
	if current < 9*time.Second || current > 11*time.Second {
		t.Fatalf("current = %s, want about 10s", current)
	}
	if remaining < 89*time.Second || remaining > 91*time.Second {
		t.Fatalf("remaining = %s, want about 1m30s", remaining)
	}
	if complete != 0 || tracked != 2 {
		t.Fatalf("complete/tracked = %d/%d, want 0/2", complete, tracked)
	}
}

// TestTaskProgressCompletionOnlyDecreasesRemaining checks countdown behavior after early and late completions.
func TestTaskProgressCompletionOnlyDecreasesRemaining(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		elapsed   time.Duration
		doneAgo   time.Duration
		remaining time.Duration
	}{
		{name: "early completion removes expected task duration", elapsed: 10 * time.Second, remaining: 70 * time.Second},
		{name: "early completion continues counting down", elapsed: 15 * time.Second, doneAgo: 5 * time.Second, remaining: 65 * time.Second},
		{name: "late completion does not increase remaining", elapsed: 45 * time.Second, remaining: 55 * time.Second},
		{name: "late completion keeps counting down from wall clock", elapsed: 50 * time.Second, doneAgo: 5 * time.Second, remaining: 50 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testTaskProgressModel([]TaskProgressItem{
				{Key: "first", Name: "first", Expected: 30 * time.Second},
				{Key: "second", Name: "second", Expected: 70 * time.Second},
			})
			m.rows["first"] = taskProgressRow{item: m.items[0], status: "done", seen: true, startedAt: now.Add(-tt.elapsed), doneAt: now.Add(-tt.doneAgo)}

			current, total, complete, tracked, remaining := m.overallProgress()

			if total != 100*time.Second {
				t.Fatalf("total = %s, want 1m40s", total)
			}
			if remaining < tt.remaining-time.Second || remaining > tt.remaining+time.Second {
				t.Fatalf("remaining = %s, want about %s", remaining, tt.remaining)
			}
			wantCurrent := total - tt.remaining
			if current < wantCurrent-time.Second || current > wantCurrent+time.Second {
				t.Fatalf("current = %s, want about %s", current, wantCurrent)
			}
			if complete != 1 || tracked != 2 {
				t.Fatalf("complete/tracked = %d/%d, want 1/2", complete, tracked)
			}
		})
	}
}

// TestTaskProgressRemainingCanGoNegativeForOvertime checks capped progress and negative remaining time for slow tasks.
func TestTaskProgressRemainingCanGoNegativeForOvertime(t *testing.T) {
	m := testTaskProgressModel([]TaskProgressItem{
		{Key: "slow", Name: "slow", Expected: 30 * time.Second},
	})
	m.rows["slow"] = taskProgressRow{item: m.items[0], status: "running", seen: true, startedAt: time.Now().Add(-35 * time.Second)}

	current, total, _, _, remaining := m.overallProgress()

	if current != total {
		t.Fatalf("current = %s, want capped total %s", current, total)
	}
	if remaining > -4*time.Second || remaining < -6*time.Second {
		t.Fatalf("remaining = %s, want about -5s", remaining)
	}
	if summary := m.timeSummary(remaining); !strings.Contains(summary, "+5s") {
		t.Fatalf("timeSummary(%s) = %q, want overtime", remaining, summary)
	}
}

// TestTaskProgressOmittedItemsDoNotContributeToRemaining checks removal of omitted tasks from timing and counts.
func TestTaskProgressOmittedItemsDoNotContributeToRemaining(t *testing.T) {
	m := testTaskProgressModel([]TaskProgressItem{
		{Key: "omitted", Name: "omitted", Expected: 30 * time.Second},
		{Key: "active", Name: "active", Expected: 70 * time.Second},
	})
	m.rows["omitted"] = taskProgressRow{item: m.items[0], status: "pending", omitted: true}
	m.rows["active"] = taskProgressRow{item: m.items[1], status: "running", seen: true, startedAt: time.Now().Add(-10 * time.Second)}

	_, total, _, tracked, remaining := m.overallProgress()

	if total != 70*time.Second {
		t.Fatalf("total = %s, want 1m10s", total)
	}
	if tracked != 1 {
		t.Fatalf("tracked = %d, want 1", tracked)
	}
	if remaining < 59*time.Second || remaining > 61*time.Second {
		t.Fatalf("remaining = %s, want about 1m", remaining)
	}
}

// testTaskProgressModel initializes pending progress rows for the supplied test plan.
func testTaskProgressModel(items []TaskProgressItem) taskProgressModel {
	m := taskProgressModel{items: items, rows: map[string]taskProgressRow{}}
	for _, item := range items {
		m.rows[item.Key] = taskProgressRow{item: item, status: "pending"}
	}
	return m
}
