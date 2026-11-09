// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"

	"github.com/podplane/podplane/internal/tfexec"
)

// typeSearch enters a query through the same keys used by a terminal reader.
func typeSearch(m tea.Model, query string) tea.Model {
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return m
}

// TestDashboardSearch navigates visible resources without approving a saved plan.
func TestDashboardSearch(t *testing.T) {
	m := planAndApplyFixture()
	p := tfexec.Plan{HasChanges: true}
	for i := range 12 {
		p.Changes = append(p.Changes, tfexec.Change{Address: fmt.Sprintf("terraform_data.node[%d]", i), Actions: []string{"create"}})
	}
	answer := make(chan bool, 1)
	m.accept(planAndApplyReview{plan: p, answer: answer})
	m.focus = 2
	m = typeSearch(m, "NODE[9]").(planAndApplyModel)
	if m.selected != 9 || m.detail || m.review == nil || len(answer) != 0 {
		t.Fatal("search did not select literal, case-insensitive match safely")
	}
	if !strings.Contains(m.View(), "node[9]") || lipgloss.Height(m.View()) != 24 {
		t.Fatal("search match scrolled out of view or resized the frame")
	}
	m = typeSearch(m, "missing").(planAndApplyModel)
	if m.selected != 9 || !strings.Contains(m.View(), "No matches") {
		t.Fatal("unsuccessful search changed selection or omitted feedback")
	}
	m.stage, m.review = "Apply", nil
	m.accept(tfexec.Event{Type: "apply_complete", Address: "terraform_data.node[9]", Action: "create"})
	m = typeSearch(m, "node[9]").(planAndApplyModel)
	if !strings.Contains(m.View(), "No matches") {
		t.Fatal("search revealed a resource hidden by the completed filter")
	}
	m.selected = 1
	m = typeSearch(m, "node").(planAndApplyModel)
	m.accept(tfexec.Event{Type: "apply_complete", Address: "terraform_data.node[0]", Action: "create"})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if next.(planAndApplyModel).selected != 2 {
		t.Fatal("live completion shifted search past the next visible resource")
	}
}

// TestSearchClear keeps search cancellation separate from closing the panel.
func TestSearchClear(t *testing.T) {
	for _, panel := range []bool{false, true} {
		m := planAndApplyFixture()
		m.logView, m.followLogs = panel, false
		m.logs = []string{"server ready"}
		var model = typeSearch(m, "server")
		model, _ = model.Update(tfexec.Event{Stage: "Apply"})
		if !strings.Contains(model.View(), "/server") {
			t.Fatal("stage transition cleared the search")
		}
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("replacement")})
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if !strings.Contains(model.View(), "/server") || strings.Contains(model.View(), "replacement") {
			t.Fatal("Esc did not cancel only the new query")
		}
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = model.(planAndApplyModel)
		if strings.Contains(m.View(), "/server") || m.tableSearch.pattern != nil || m.panelSearch.pattern != nil || m.logView != panel {
			t.Fatal("clearing search retained highlights or closed the panel")
		}
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if model.(planAndApplyModel).logView {
			t.Fatal("Esc without search did not close the panel")
		}
	}
}

// TestPanelSearch checks wrapping, reverse navigation, and pause/resume in each text panel.
func TestPanelSearch(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	for _, i := range []int{2, 26, 29} {
		lines[i] += " café [match]"
	}
	for _, panel := range []string{"logs", "details", "outputs"} {
		t.Run(panel, func(t *testing.T) {
			m := planAndApplyFixture()
			m.logs, m.logView, m.detail, m.outputView = lines, panel == "logs", panel == "details", panel == "outputs"
			m.followLogs = m.logView
			m.plan.Outputs = strings.Join(lines, "\n")
			m.rows[m.plan.Changes[0].Address].change.Details = strings.Join(lines, "\n")
			m = typeSearch(m, "CAFÉ [match]").(planAndApplyModel)
			first := 2
			if panel == "logs" {
				first = 26
			}
			if m.scroll != first || m.followLogs {
				t.Fatalf("first match = %d, following = %v", m.scroll, m.followLogs)
			}
			for _, want := range []int{26, 29, 2} {
				if panel == "logs" && want == 26 {
					continue
				}
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
				m = next.(planAndApplyModel)
				if m.scroll != want {
					t.Fatalf("next match = %d, want %d", m.scroll, want)
				}
			}
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
			m = next.(planAndApplyModel)
			if m.scroll != 29 {
				t.Fatal("reverse search did not wrap")
			}
			if lipgloss.Height(m.View()) > 24 || lipgloss.Width(m.View()) > 80 {
				t.Fatal("search overflowed the modal")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
			next, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(planAndApplyModel)
			if m.panelSearch.editing || !m.logView && !m.detail && !m.outputView {
				t.Fatal("Esc closed panel instead of search")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
			if panel == "logs" && !next.(planAndApplyModel).followLogs {
				t.Fatal("End did not resume following")
			}
		})
	}
}

// TestChoiceSearch ensures search input cannot approve or toggle checklist/list choices.
func TestChoiceSearch(t *testing.T) {
	m, err := newChecklistModel(ChecklistOptions{Items: []ChecklistItem{{Label: "Keep", Checked: true}, {Label: "Delete logs", Checked: true}}})
	if err != nil {
		t.Fatal(err)
	}
	m.focus = 2
	m = typeSearch(m, "logs").(checklistModel)
	if m.selected != 1 || m.approved || !slices.Equal(m.checked, []bool{true, true}) {
		t.Fatal("search changed checkbox values or approved deletion")
	}
	l := list.New([]list.Item{Item{Key: "a", Label: "Alpha"}, Item{Key: "b", Label: "Beta"}}, itemDelegate{}, 74, 14)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
	choice := typeSearch(model{list: l}, "Beta").(model)
	if choice.choice != "" || choice.quitting || choice.list.Index() != 1 {
		t.Fatal("search submitted instead of navigating the list")
	}
	flow := flowModel{width: 80, height: 24, title: "Choose", stage: "Confirm", model: choice}
	if lipgloss.Height(flow.View()) != 24 {
		t.Fatal("list help overflowed the flow frame")
	}
}

// TestPreparationLogSearch keeps letter shortcuts and Esc local to search entry.
func TestPreparationLogSearch(t *testing.T) {
	m := flowModel{width: 80, height: 24, title: "Prepare", logs: []string{"hello", "world"}}
	var updated tea.Model = m
	for _, text := range []string{"l", "/", "hello"} {
		updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	}
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(flowModel)
	if m.logPanel == nil || m.logPanel.panelSearch.editing {
		t.Fatal("search entry closed preparation logs")
	}
}

// TestSearchEscapeRoutes preserves cancellation and Inline handoff during search entry.
func TestSearchEscapeRoutes(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyCtrlO} {
		m := planAndApplyFixture()
		cancelled := false
		m.cancel = func() { cancelled = true }
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
		next, cmd := next.Update(tea.KeyMsg{Type: key})
		m = next.(planAndApplyModel)
		if key == tea.KeyCtrlC && (!cancelled || !m.stopping) {
			t.Fatal("search swallowed cancellation")
		}
		if key == tea.KeyCtrlO && (!m.inlineMode || cmd == nil || cancelled) {
			t.Fatal("search blocked Inline handoff or cancelled execution")
		}
	}
}

// TestShortcutLayout ensures emphasis does not change text or introduce multiline padding.
func TestShortcutLayout(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	got := ansi.Strip(Shortcuts("`Enter` Details · `l` Logs\n`Ctrl+O` Inline mode"))
	if got != "Enter Details · l Logs\nCtrl+O Inline mode" {
		t.Fatalf("shortcut layout = %q", got)
	}
}

// TestSearchStyledText keeps existing formatting codes out of visible search matches.
func TestSearchStyledText(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := planAndApplyFixture()
	m.detail = true
	m.rows[m.plan.Changes[0].Address].change.Details = "Operation: create\n\nBefore\nnull\n\nAfter\nknown after apply"
	m = typeSearch(m, "after").(planAndApplyModel)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "After") || strings.Contains(view, "[1m") || strings.Contains(view, "[0m") {
		t.Fatalf("search leaked ANSI formatting into text:\n%s", view)
	}
}

// TestSearchRowBackground keeps selection colour across matches and trailing spaces.
func TestSearchRowBackground(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		lipgloss.SetHasDarkBackground(dark)
	})
	for _, panel := range []bool{false, true} {
		m := planAndApplyFixture()
		needle, width := "> module.cluster", 74
		suffix := "Replace"
		if panel {
			m.logView, m.followLogs = true, false
			m.logs = []string{"prefix server middle server suffix"}
			needle, width = "prefix server", 68
			suffix = "middle server suffix"
		}
		m = typeSearch(m, "server").(planAndApplyModel)
		found := false
		for _, line := range strings.Split(m.View(), "\n") {
			start := strings.Index(ansi.Strip(line), needle)
			if start < 0 {
				continue
			}
			found = true
			if !strings.Contains(ansi.Strip(line), suffix) {
				t.Fatalf("panel=%t: search truncated the row: %q", panel, line)
			}
			x := ansi.StringWidth(ansi.Strip(line)[:start])
			cells := cellbuf.NewBuffer(80, 1)
			cellbuf.SetContent(cells, line)
			for column := x; column < x+width; column++ {
				cell := cells.Cell(column, 0)
				if cell == nil || cell.Style.Bg == nil || cell.Style.Bg != cells.Cell(x, 0).Style.Bg {
					t.Fatalf("panel=%t: selection background missing at column %d: %#v; %q", panel, column, cell, line)
				}
			}
		}
		if !found {
			t.Fatalf("panel=%t: selected search result missing", panel)
		}
	}
}
