// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestChecklistDependencies checks prerequisite enforcement and explicit visible approval.
func TestChecklistDependencies(t *testing.T) {
	labels := []string{"Infrastructure", "Cluster buckets {" + strings.Repeat("a", 63) + ", " + strings.Repeat("b", 63) + ", " + strings.Repeat("c", 63) + "}", "State bucket {" + strings.Repeat("s", 63) + "}", "Terraform files", "Config"}
	items := make([]ChecklistItem, len(labels))
	for i, label := range labels {
		items[i] = ChecklistItem{Label: label, Checked: true}
		if i > 0 {
			items[i].Requires = []int{i - 1}
		}
	}
	m, err := newChecklistModel(ChecklistOptions{Title: "Delete cluster", Items: items,
		ConfirmLabel: "Delete selected", Description: "Unchecked actions keep their data and recovery files.\nDeletion is permanent. Later steps require earlier steps."})
	if err != nil {
		t.Fatal(err)
	}
	key := func(k tea.KeyType) tea.Cmd {
		updated, cmd := m.Update(tea.KeyMsg{Type: k})
		m = updated.(checklistModel)
		return cmd
	}
	if lipgloss.Width(m.View()) > 80 || lipgloss.Height(m.View()) > 24 {
		t.Fatalf("checklist overflow:\n%s", m.View())
	}
	for i := range items {
		embedded := m
		embedded.embedded, embedded.width, embedded.height, embedded.selected = true, 74, 18, i
		view := embedded.View()
		if lipgloss.Height(view) > 18 || lipgloss.Width(view) > 74 || !strings.Contains(view, "Delete selected") {
			t.Fatalf("embedded checklist hides actions at row %d:\n%s", i, view)
		}
	}
	key(tea.KeyDown)
	key(tea.KeySpace)
	if !slices.Equal(m.checked, []bool{true, false, false, false, false}) {
		t.Fatal("unchecking buckets did not clear dependent actions")
	}
	key(tea.KeyDown)
	key(tea.KeyDown)
	key(tea.KeySpace)
	if m.checked[3] {
		t.Fatal("Terraform files selected without prerequisites")
	}
	key(tea.KeyDown)
	key(tea.KeySpace)
	if m.checked[4] {
		t.Fatal("config selected without prerequisites")
	}
	key(tea.KeyUp)
	key(tea.KeyUp)
	key(tea.KeyUp)
	key(tea.KeySpace)
	if !slices.Equal(m.checked, []bool{true, true, false, false, false}) {
		t.Fatal("rechecking bucket silently reselected later cleanup")
	}
	if cmd := key(tea.KeyEnter); cmd != nil || m.approved {
		t.Fatal("Enter on row approved deletion")
	}
	key(tea.KeyTab)
	key(tea.KeyShiftTab)
	if m.focus != 0 {
		t.Fatal("reverse Tab did not return to checklist")
	}
	key(tea.KeyTab)
	key(tea.KeyTab)
	if !strings.Contains(m.View(), "> [ Delete selected ]") {
		t.Fatal("delete action not visibly focused")
	}
	m.width = 60
	if key(tea.KeyEnter) != nil || m.approved {
		t.Fatal("small terminal approved hidden selection")
	}
	m.width = 80
	if key(tea.KeyEnter) == nil || !m.approved {
		t.Fatal("explicit approval failed")
	}
}

// TestChecklistIndependentChoices checks branching prerequisites without coupling independent items.
func TestChecklistIndependentChoices(t *testing.T) {
	opts := ChecklistOptions{Title: "Choose features", Items: []ChecklistItem{
		{Label: "Core", Checked: true},
		{Label: "Metrics"},
		{Label: "Dashboard", Checked: true, Requires: []int{0}},
		{Label: "Charts", Checked: true, Requires: []int{0, 1}},
		{Label: "Theme", Checked: true, Requires: []int{2}},
		{Label: "Documentation", Checked: true},
	}}
	m, err := newChecklistModel(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		item int
		want []bool
	}{
		{-1, []bool{true, false, true, false, true, true}},
		{0, []bool{false, false, false, false, false, true}},
		{0, []bool{true, false, false, false, false, true}},
		{3, []bool{true, false, false, false, false, true}},
		{1, []bool{true, true, false, false, false, true}},
		{3, []bool{true, true, false, true, false, true}},
	} {
		if step.item >= 0 {
			m.selected = step.item
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
			m = updated.(checklistModel)
		}
		if !slices.Equal(m.checked, step.want) {
			t.Fatalf("toggle %d: got %v, want %v", step.item, m.checked, step.want)
		}
	}
	if strings.Contains(m.View(), "delet") || !strings.Contains(m.View(), "[ Continue ]") {
		t.Fatal("generic checklist contains deletion-specific controls")
	}

	t.Setenv("CI", "true")
	for _, scenario := range []struct {
		name, input string
		auto        bool
		want        []bool
	}{
		{"defaults", "\n\n\n\n\nyes\n", false, []bool{true, false, true, false, true, true}},
		{"branch", "n\ny\n\nyes\n", false, []bool{false, true, false, false, false, true}},
		{"auto", "", true, []bool{true, false, true, false, true, true}},
		{"cancel", "\n\n\n\n\nno\n", false, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin := os.Stdin
			os.Stdin = reader
			t.Cleanup(func() { os.Stdin = stdin; _ = reader.Close(); _ = writer.Close() })
			if _, err := writer.WriteString(scenario.input); err != nil {
				t.Fatal(err)
			}
			_ = writer.Close()
			opts.AutoApprove = scenario.auto
			got, err := Checklist(context.Background(), opts)
			if (err != nil) != (scenario.want == nil) || !slices.Equal(got, scenario.want) {
				t.Fatalf("selection = %v, %v; want %v", got, err, scenario.want)
			}
		})
	}
}

// TestChecklistInvalidDependencies rejects invalid indices and cycles before prompting.
func TestChecklistInvalidDependencies(t *testing.T) {
	for _, items := range [][]ChecklistItem{
		nil,
		{{Label: "Self", Requires: []int{0}}},
		{{Label: "Negative", Requires: []int{-1}}},
		{{Label: "Missing", Requires: []int{3}}},
		{{Label: "Later", Requires: []int{1}}, {Label: "Earlier", Requires: []int{0}}},
	} {
		if _, err := Checklist(context.Background(), ChecklistOptions{Items: items, AutoApprove: true}); err == nil {
			t.Fatalf("accepted invalid items: %v", items)
		}
	}
}
