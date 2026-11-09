// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/podplane/podplane/internal/tfexec"
)

// TestPlanAndApplyPlainApprovalCancellation verifies that a cancelled prompt does
// not leave a reader consuming the next plan's approval input.
func TestPlanAndApplyPlainApprovalCancellation(t *testing.T) {
	t.Setenv("CI", "true")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = stdin; _ = reader.Close(); _ = writer.Close() })
	run := func(ctx context.Context, emit func(tfexec.Event), review func(tfexec.Plan) (bool, error)) error {
		emit(tfexec.Event{Stage: "Review"})
		ok, err := review(tfexec.Plan{HasChanges: true})
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("not approved")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := RunPlanAndApply(ctx, PlanAndApplyOptions{}, run); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled review: %v", err)
	}
	if _, err := writer.WriteString("yes\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancelNext := context.WithTimeout(context.Background(), time.Second)
	defer cancelNext()
	if err := RunPlanAndApply(ctx, PlanAndApplyOptions{}, run); err != nil {
		t.Fatalf("subsequent approval: %v", err)
	}
}

// TestPlanAndApplyReviewKeyboard verifies Enter inspects instead of approving,
// explicit action focus, safe cancellation, and the small-terminal escape route.
func TestPlanAndApplyReviewKeyboard(t *testing.T) {
	for _, approve := range []bool{false, true} {
		m := planAndApplyFixture()
		answer := m.review.answer
		key := func(k tea.KeyType) {
			t.Helper()
			updated, _ := m.Update(tea.KeyMsg{Type: k})
			m = updated.(planAndApplyModel)
		}
		key(tea.KeyEnter)
		if !strings.Contains(m.View(), "Resource details") {
			t.Fatal("Enter did not open details")
		}
		select {
		case <-answer:
			t.Fatal("inspecting a row approved the plan")
		default:
		}
		key(tea.KeyEsc)
		key(tea.KeyTab)
		key(tea.KeyShiftTab)
		key(tea.KeyEnter)
		if !strings.Contains(m.View(), "Resource details") {
			t.Fatal("Shift+Tab from Cancel did not return to the table")
		}
		key(tea.KeyEsc)
		key(tea.KeyShiftTab)
		if !strings.Contains(m.View(), "> [ Approve and apply ]") {
			t.Fatal("Shift+Tab from the table did not wrap to Approve")
		}
		key(tea.KeyShiftTab)
		if !strings.Contains(m.View(), "> [ Cancel ]") {
			t.Fatal("Shift+Tab from Approve did not return to Cancel")
		}
		if approve {
			key(tea.KeyTab)
		}
		key(tea.KeyEnter)
		select {
		case got := <-answer:
			if got != approve {
				t.Fatalf("approval = %v", got)
			}
		default:
			t.Fatal("action did not answer review")
		}
	}
	m := planAndApplyFixture()
	m.width = 64
	if !strings.Contains(m.View(), "waiting for approval") {
		t.Fatal("undersized review hid approval state")
	}
	m.focus = 2
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(planAndApplyModel)
	select {
	case <-m.review.answer:
		t.Fatal("undersized screen approved unseen plan")
	default:
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !updated.(planAndApplyModel).plain {
		t.Fatal("plain output escape did not work")
	}
}

// TestPlanAndApplyProgressAndLayout checks parallel replacement accounting and all
// supported views at the minimum terminal size, including long diagnostics.
func TestPlanAndApplyProgressAndLayout(t *testing.T) {
	m := planAndApplyFixture()
	if strings.Contains(m.View(), "Status") || strings.Contains(m.View(), "Waiting") || strings.Contains(m.View(), ">○") {
		t.Fatal("review shows execution status before apply")
	}
	m.review = nil
	m.stage = "Apply"
	if !strings.Contains(m.View(), "Status") || !strings.Contains(m.View(), "Waiting") {
		t.Fatal("apply is missing execution status")
	}
	address := m.plan.Changes[0].Address
	m.accept(tfexec.Event{Type: "apply_complete", Address: address, Action: "delete"})
	if strings.Contains(m.View(), "100%") {
		t.Fatal("replacement completed after deletion alone")
	}
	m.accept(tfexec.Event{Type: "apply_start", Address: address, Action: "create"})
	m.accept(tfexec.Event{Type: "apply_complete", Address: address, Action: "create"})
	if !strings.Contains(m.View(), "100%") {
		t.Fatal("replacement did not complete")
	}
	for _, state := range []string{"apply", "review", "details", "logs", "outputs", "failure", "wrapped activity", "long activity"} {
		t.Run(state, func(t *testing.T) {
			view := m
			switch state {
			case "review":
				view = planAndApplyFixture()
			case "details":
				view.detail = true
			case "logs":
				view.logView = true
				view.logs = []string{strings.Repeat("long diagnostic ", 50)}
			case "outputs":
				view.outputView = true
			case "failure":
				view.accept(planAndApplyDone{fmt.Errorf("engine failed")})
			case "wrapped activity":
				view.accept(tfexec.Event{Message: strings.Repeat("resource_", 12) + ": Creation complete after 1s [id=rtb-123]"})
				if !strings.Contains(view.View(), "[id=rtb-123]") {
					t.Fatal("wrapped activity lost its final line")
				}
			case "long activity":
				view.accept(tfexec.Event{Message: strings.Repeat("long diagnostic ", 100)})
				if !strings.Contains(view.View(), "Press l for full logs") {
					t.Fatal("long activity omitted the full logs hint")
				}
			}
			text := view.View()
			if lipgloss.Width(text) > 80 || lipgloss.Height(text) > 24 {
				t.Fatalf("%dx%d exceeds 80x24:\n%s", lipgloss.Width(text), lipgloss.Height(text), text)
			}
		})
	}
}

// TestPlanAndApplyLogNavigation checks paging, live following, and paused log retention.
func TestPlanAndApplyLogNavigation(t *testing.T) {
	m := planAndApplyFixture()
	// Wrapped lines ensure navigation counts displayed lines, not log entries.
	m.logs = []string{strings.Repeat("x", 136)}
	for i := 1; i <= 35; i++ {
		m.logs = append(m.logs, fmt.Sprintf("entry-%02d", i))
	}
	key := func(k tea.KeyMsg) {
		t.Helper()
		updated, _ := m.Update(k)
		m = updated.(planAndApplyModel)
	}
	runeKey := func(r rune) { key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}) }
	check := func(text string) {
		t.Helper()
		if !strings.Contains(m.View(), text) {
			t.Fatalf("missing %q:\n%s", text, m.View())
		}
	}
	runeKey('l')
	check("28–37/37 · Following")
	for _, top := range []tea.KeyMsg{{Type: tea.KeyHome}, {Type: tea.KeyRunes, Runes: []rune{'g'}}} {
		key(top)
		check("1–10/37 · Paused")
		key(tea.KeyMsg{Type: tea.KeyPgDown})
		check("11–20/37 · Paused")
		key(tea.KeyMsg{Type: tea.KeySpace})
		check("21–30/37 · Paused")
		key(tea.KeyMsg{Type: tea.KeyPgUp})
		check("11–20/37 · Paused")
		runeKey('b')
		check("1–10/37 · Paused")
		key(tea.KeyMsg{Type: tea.KeyCtrlF})
		check("11–20/37 · Paused")
		key(tea.KeyMsg{Type: tea.KeyCtrlB})
		check("1–10/37 · Paused")
	}
	key(tea.KeyMsg{Type: tea.KeyEnd})
	check("28–37/37 · Following")
	m.accept(tfexec.Event{Message: "live-36"})
	check("29–38/38 · Following")
	key(tea.KeyMsg{Type: tea.KeyUp})
	check("28–37/38 · Paused")
	m.accept(tfexec.Event{Message: "live-37"})
	check("28–37/39 · Paused")
	runeKey('G')
	check("30–39/39 · Following")
	// Repeated page-down at the end must not accumulate an invisible offset.
	key(tea.KeyMsg{Type: tea.KeyHome})
	for i := 0; i < 10; i++ {
		key(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	key(tea.KeyMsg{Type: tea.KeyUp})
	check("29–38/39 · Paused")
	// A long run retains its first entry and preserves the paused viewport.
	m.logs = m.logs[:1]
	for i := 1; i < 1000; i++ {
		m.logs = append(m.logs, fmt.Sprintf("entry-%04d", i))
	}
	key(tea.KeyMsg{Type: tea.KeyHome})
	key(tea.KeyMsg{Type: tea.KeyPgDown})
	check("entry-0009")
	m.accept(tfexec.Event{Message: "entry-1000"})
	check("entry-0009")
	check("11–20/1002 · Paused")
	key(tea.KeyMsg{Type: tea.KeyHome})
	check(strings.Repeat("x", 68))
	key(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(m.View(), "Engine logs") {
		t.Fatal("Esc did not close logs")
	}
}

// TestPlanAndApplyLogBudget checks that oversized history retains recent entries and reports loss.
func TestPlanAndApplyLogBudget(t *testing.T) {
	m := planAndApplyFixture()
	// 64 one-MiB entries, including their overhead, exceed the budget.
	for i := 0; i < 64; i++ {
		m.accept(tfexec.Event{Message: fmt.Sprintf("%02d", i) + strings.Repeat("x", (1<<20)-2)})
	}
	if len(m.logs) != 63 || !strings.HasPrefix(m.logs[0], "01") || !strings.HasPrefix(m.logs[62], "63") {
		t.Fatal("log budget did not retain the newest entries")
	}
	if !m.logsDiscarded || m.logBytes > planAndApplyLogBudget {
		t.Fatal("log budget exceeded or discarded output not reported")
	}
}

// TestPlanAndApplyDoneKeyboard checks visible focus and dismissal after success or failure.
func TestPlanAndApplyDoneKeyboard(t *testing.T) {
	for _, failure := range []error{nil, errors.New("apply failed")} {
		m := planAndApplyFixture()
		m.review = nil
		m.accept(planAndApplyDone{err: failure})
		for _, direction := range []tea.KeyType{tea.KeyTab, tea.KeyShiftTab} {
			for _, focused := range []bool{true, false, true} {
				updated, _ := m.Update(tea.KeyMsg{Type: direction})
				m = updated.(planAndApplyModel)
				if strings.Contains(m.View(), "> [ Close ]") != focused {
					t.Fatal("Tab did not cycle between the table and highlighted Close")
				}
			}
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("focused Close did not quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("focused Close did not quit")
			}
			m.focus = 0
		}
	}
}

// TestPlanAndApplyPanelMouse checks panel-local scrolling and paused live logs.
func TestPlanAndApplyPanelMouse(t *testing.T) {
	for _, panel := range []string{"logs", "details", "outputs"} {
		t.Run(panel, func(t *testing.T) {
			m := planAndApplyFixture()
			text := strings.Repeat("content\n", 30)
			m.logs = []string{text}
			m.plan.Outputs = text
			m.rows[m.plan.Changes[0].Address].change.Details = text
			m.logView, m.detail, m.outputView = panel == "logs", panel == "details", panel == "outputs"
			pageSize := 10
			if panel == "details" {
				pageSize = 5 // Fixed resource metadata occupies the other five lines.
			}
			mouse := func(button tea.MouseButton, x int) {
				updated, _ := m.Update(tea.MouseMsg{Button: button, Action: tea.MouseActionPress, X: x, Y: 10})
				m = updated.(planAndApplyModel)
			}
			mouse(tea.MouseButtonWheelDown, 5)
			if !strings.Contains(m.View(), fmt.Sprintf("2–%d/", pageSize+1)) {
				t.Fatal("wheel down did not scroll panel")
			}
			mouse(tea.MouseButtonWheelDown, 0)
			if !strings.Contains(m.View(), fmt.Sprintf("2–%d/", pageSize+1)) {
				t.Fatal("wheel outside panel scrolled it")
			}
			mouse(tea.MouseButtonWheelUp, 5)
			mouse(tea.MouseButtonWheelUp, 5)
			if !strings.Contains(m.View(), fmt.Sprintf("1–%d/", pageSize)) {
				t.Fatal("wheel up did not stop at top")
			}
			if panel == "logs" {
				m.followLogs = true
				mouse(tea.MouseButtonWheelUp, 5)
				if !strings.Contains(m.View(), "21–30/31 · Paused") {
					t.Fatal("wheel up did not pause at live log position")
				}
				m.accept(tfexec.Event{Message: "new log"})
				if !strings.Contains(m.View(), "21–30/32 · Paused") {
					t.Fatal("new log moved paused viewport")
				}
				updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
				m = updated.(planAndApplyModel)
				if !strings.Contains(m.View(), "23–32/32 · Following") {
					t.Fatal("End did not resume following")
				}
			}
		})
	}
}

// TestPlanAndApplyLogSelection checks bidirectional text selection and bounded clipboard requests.
func TestPlanAndApplyLogSelection(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		m := planAndApplyFixture()
		m.logView, m.followLogs = true, true
		m.logs = []string{"alpha βeta", "second line", "last line"}
		fromX, fromY, toX, toY := 7, 5, 10, 6
		if reverse {
			fromX, fromY, toX, toY = toX, toY, fromX, fromY
		}
		updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: fromX, Y: fromY})
		m = updated.(planAndApplyModel)
		m.accept(tfexec.Event{Message: "arrived during selection"})
		updated, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: toX, Y: toY})
		m = updated.(planAndApplyModel)
		if cmd == nil || m.selection.text() != "pha βeta\nsecond" || m.followLogs {
			t.Fatal("selection did not pause and copy the selected text")
		}
		if strings.Contains(m.View(), "arrived during selection") {
			t.Fatal("live output changed selected text")
		}
		updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
		m = updated.(planAndApplyModel)
		if cmd == nil || m.selection != nil || !strings.Contains(m.View(), "Copying") {
			t.Fatal("copy logs did not clear selection and request copy")
		}
		updated, _ = m.Update(planAndApplyCopied{errors.New(strings.Repeat("clipboard unavailable ", 20))})
		m = updated.(planAndApplyModel)
		if !strings.Contains(m.View(), "Copy failed") || lipgloss.Height(m.View()) > 24 || lipgloss.Width(m.View()) > 80 {
			t.Fatal("clipboard failure missing or overflows panel")
		}
	}
	result := copyPlanAndApplyLogs(strings.Repeat("x", 4*1024*1024+1))().(planAndApplyCopied)
	if result.err == nil {
		t.Fatal("oversize clipboard write was not rejected")
	}
}

// TestPlanAndApplyDetailsFixedHeader checks that scrolling keeps resource identity visible.
func TestPlanAndApplyDetailsFixedHeader(t *testing.T) {
	m := planAndApplyFixture()
	m.detail = true
	address := m.plan.Changes[0].Address
	m.rows[address].change.Details = "Operation: delete → create\n\nBefore\nnull\n\nAfter\n{\n" + strings.Repeat("  field: value\n", 20) + "  final: value\n}"
	before := strings.Split(m.View(), "\n")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(planAndApplyModel)
	after := strings.Split(m.View(), "\n")
	for _, label := range []string{address, "Status: Waiting", "Operation: delete → create"} {
		found := false
		for i, line := range before {
			if strings.Contains(line, label) {
				found = true
				if !strings.Contains(after[i], label) {
					t.Fatalf("scroll moved fixed header %q", label)
				}
			}
		}
		if !found {
			t.Fatalf("missing header %q", label)
		}
	}
	if !strings.Contains(m.View(), "final: value") || strings.Contains(m.View(), "Before") {
		t.Fatal("End did not scroll the diff independently")
	}
	if lipgloss.Height(m.View()) > 24 || lipgloss.Width(m.View()) > 80 {
		t.Fatal("fixed header overflowed minimum terminal")
	}
}

// TestPlanAndApplyTablePaging checks shared paging keys without implicitly approving a plan.
func TestPlanAndApplyTablePaging(t *testing.T) {
	for _, stage := range []string{"Review", "Apply"} {
		for _, keys := range [][2]tea.KeyMsg{
			{{Type: tea.KeyPgDown}, {Type: tea.KeyPgUp}},
			{{Type: tea.KeyCtrlF}, {Type: tea.KeyCtrlB}},
			{{Type: tea.KeySpace}, {Type: tea.KeyRunes, Runes: []rune{'b'}}},
		} {
			m := planAndApplyFixture()
			m.height = 26 // Six table rows plus the fixed three-line activity panel.
			changes := make([]tfexec.Change, 15)
			for i := range changes {
				changes[i] = tfexec.Change{Address: fmt.Sprintf("resource-%02d", i), Actions: []string{"create"}}
			}
			m.accept(planAndApplyReview{plan: tfexec.Plan{Changes: changes, HasChanges: true}, answer: make(chan bool, 1)})
			m.stage = stage
			for _, step := range []struct{ direction, selected int }{{0, 6}, {0, 12}, {0, 14}, {1, 8}, {1, 2}, {1, 0}} {
				updated, _ := m.Update(keys[step.direction])
				m = updated.(planAndApplyModel)
				if !strings.Contains(m.View(), fmt.Sprintf(">%s resource-%02d", map[bool]string{true: "○", false: ""}[stage == "Apply"], step.selected)) {
					t.Fatalf("%s %s did not select resource %d:\n%s", stage, keys[step.direction], step.selected, m.View())
				}
				select {
				case <-m.review.answer:
					t.Fatal("paging answered the approval prompt")
				default:
				}
			}
		}
	}
}

// TestPlanAndApplyTableMouse checks selection, resource-text hit targets, and modal dismissal.
func TestPlanAndApplyTableMouse(t *testing.T) {
	for _, stage := range []string{"Review", "Apply"} {
		m := planAndApplyFixture()
		m.height = 26 // Six table rows plus the fixed three-line activity panel.
		changes := make([]tfexec.Change, 15)
		for i := range changes {
			changes[i] = tfexec.Change{Address: fmt.Sprintf("resource-%02d", i), Actions: []string{"create"}}
		}
		m.accept(planAndApplyReview{plan: tfexec.Plan{Changes: changes, HasChanges: true}, answer: make(chan bool, 1)})
		m.stage = stage
		// Locate the rendered row rather than duplicating the hit-test offset.
		row := 0
		for y, line := range strings.Split(m.View(), "\n") {
			if strings.Contains(line, "resource-00") {
				row = y
				break
			}
		}
		mouse := func(button tea.MouseButton, action tea.MouseAction, x, y int) {
			t.Helper()
			updated, _ := m.Update(tea.MouseMsg{Button: button, Action: action, X: x, Y: y})
			m = updated.(planAndApplyModel)
		}
		for i := 0; i < 8; i++ {
			mouse(tea.MouseButtonWheelDown, tea.MouseActionPress, 5, row)
		}
		if !strings.Contains(m.View(), "Rows 4–9 of 15") {
			t.Fatalf("wheel did not scroll table:\n%s", m.View())
		}
		for i := 0; i < 5; i++ {
			mouse(tea.MouseButtonWheelUp, tea.MouseActionPress, 5, row)
			if !strings.Contains(m.View(), "Rows 4–9 of 15") || m.selected != 7-i {
				t.Fatal("scrolling up moved the viewport before selection reached its top")
			}
		}
		mouse(tea.MouseButtonWheelUp, tea.MouseActionPress, 5, row)
		if !strings.Contains(m.View(), "Rows 3–8 of 15") {
			t.Fatal("wheel up did not scroll back")
		}
		for _, pos := range [][2]int{{0, row}, {3, row}, {20, row}, {70, row}, {79, row}, {5, row - 1}, {5, row + 6}} {
			mouse(tea.MouseButtonLeft, tea.MouseActionPress, pos[0], pos[1])
			if m.detail {
				t.Fatal("click outside resource text opened details")
			}
		}
		mouse(tea.MouseButtonNone, tea.MouseActionMotion, 5, row+2)
		if m.detail || m.selected != 2 {
			t.Fatal("hover opened details or changed keyboard selection")
		}
		mouse(tea.MouseButtonLeft, tea.MouseActionRelease, 5, row+2)
		if m.detail {
			t.Fatal("release opened details")
		}
		mouse(tea.MouseButtonLeft, tea.MouseActionPress, 5, row+2)
		if !m.detail || !strings.Contains(m.View(), "Resource details") || m.plan.Changes[m.selected].Address != "resource-04" {
			t.Fatalf("click opened wrong resource:\n%s", m.View())
		}
		mouse(tea.MouseButtonWheelDown, tea.MouseActionPress, 5, row)
		mouse(tea.MouseButtonLeft, tea.MouseActionPress, 5, row)
		if m.plan.Changes[m.selected].Address != "resource-04" {
			t.Fatal("mouse interacted with table underneath modal")
		}
		mouse(tea.MouseButtonLeft, tea.MouseActionPress, 0, row)
		if m.detail || m.plan.Changes[m.selected].Address != "resource-04" {
			t.Fatal("outside click did not dismiss details without changing selection")
		}
		select {
		case <-m.review.answer:
			t.Fatal("mouse approved plan")
		default:
		}
		m.detail = false
		m.width = 60
		mouse(tea.MouseButtonLeft, tea.MouseActionPress, 5, row)
		if m.detail {
			t.Fatal("small terminal opened hidden row")
		}
		m = planAndApplyFixture()
		mouse(tea.MouseButtonLeft, tea.MouseActionPress, 5, row+1)
		if m.detail {
			t.Fatal("empty row opened details")
		}
	}
}

// TestPlanAndApplyHideCompleted checks stable ordering, selection, and totals while filtering.
func TestPlanAndApplyHideCompleted(t *testing.T) {
	m := planAndApplyFixture()
	changes := []tfexec.Change{}
	for _, name := range []string{"alpha", "beta", "gamma", "delta"} {
		changes = append(changes, tfexec.Change{Address: name, Actions: []string{"create"}})
	}
	m.accept(planAndApplyReview{plan: tfexec.Plan{Changes: changes, HasChanges: true}, answer: make(chan bool, 1)})
	key := func(k tea.KeyMsg) {
		updated, _ := m.Update(k)
		m = updated.(planAndApplyModel)
	}
	toggle := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}
	key(toggle)
	if m.hideCompleted {
		t.Fatal("filter should not activate in review")
	}
	m.stage, m.review = "Apply", nil
	m.accept(tfexec.Event{Type: "apply_complete", Address: "alpha", Action: "create"})
	m.accept(tfexec.Event{Type: "apply_start", Address: "beta"})
	m.accept(tfexec.Event{Type: "apply_errored", Address: "delta"})
	if !strings.Contains(m.View(), "alpha") {
		t.Fatal("completed rows hidden by default")
	}
	key(toggle)
	view := m.View()
	if strings.Contains(view, "alpha") || !strings.Contains(view, "1/4 complete") || !strings.Contains(view, "1 active · 1 failed") {
		t.Fatalf("filter changed progress or retained completed row:\n%s", view)
	}
	if strings.Index(view, "beta") >= strings.Index(view, "gamma") || strings.Index(view, "gamma") >= strings.Index(view, "delta") {
		t.Fatal("filter reordered plan")
	}
	// Click the second visible resource, not the second resource in the plan.
	updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: 11})
	m = updated.(planAndApplyModel)
	if !m.detail || m.selected != 2 {
		t.Fatal("filtered click opened wrong resource")
	}
	m.accept(tfexec.Event{Type: "apply_complete", Address: "gamma", Action: "create"})
	if m.selected != 2 || !strings.Contains(m.View(), "gamma") {
		t.Fatal("completion changed open details resource")
	}
	key(tea.KeyMsg{Type: tea.KeyEsc})
	if m.selected != 3 {
		t.Fatal("selection did not advance to next unfinished resource")
	}
	key(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.selected != 1 {
		t.Fatal("paging did not skip hidden resources")
	}
	m.accept(tfexec.Event{Type: "apply_complete", Address: "beta", Action: "create"})
	m.accept(tfexec.Event{Type: "apply_complete", Address: "delta", Action: "create"})
	if !strings.Contains(m.View(), "No unfinished resources") || !strings.Contains(m.View(), "4/4 complete") {
		t.Fatal("empty filtered table lost completion state")
	}
	key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.detail {
		t.Fatal("empty table opened hidden details")
	}
	key(toggle)
	if !strings.Contains(m.View(), "alpha") || !strings.Contains(m.View(), "delta") {
		t.Fatal("show completed did not restore rows")
	}
	if lipgloss.Width(m.View()) > 80 || lipgloss.Height(m.View()) > 24 {
		t.Fatal("filter controls overflowed minimum terminal")
	}
}

// TestPlanAndApplyActivityStableLayout checks that message length cannot move the resource table.
func TestPlanAndApplyActivityStableLayout(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := planAndApplyFixture()
		m.width = width
		rowCountLine := -1
		for _, message := range []string{"", "Complete", "First\nSecond", "First\nSecond\nThird", strings.Repeat("long diagnostic ", 100)} {
			m.logs = []string{message}
			view := m.View()
			found := false
			for i, line := range strings.Split(view, "\n") {
				if strings.Contains(line, "Rows ") {
					found = true
					if rowCountLine == -1 {
						rowCountLine = i
					} else if rowCountLine != i {
						t.Fatalf("activity resized the table at width %d", width)
					}
				}
			}
			if !found || lipgloss.Height(view) > 24 {
				t.Fatal("activity hid the table or overflowed the terminal")
			}
		}
	}
}

// TestPlanAndApplyEmptyStages checks compact progress before resources are available.
func TestPlanAndApplyEmptyStages(t *testing.T) {
	for _, stage := range []string{"Init", "Plan"} {
		m := planAndApplyFixture()
		m.stage, m.plan, m.review = stage, tfexec.Plan{}, nil
		m.logs = []string{"Downloading provider"}
		view := m.View()
		for _, hidden := range []string{"Resource", "Rows", "0 create", "Select", "Outputs"} {
			if strings.Contains(view, hidden) {
				t.Fatalf("empty %s shows %q", stage, hidden)
			}
		}
		if !strings.Contains(view, "Downloading provider") || lipgloss.Height(view) > 12 {
			t.Fatalf("empty stage did not show compact progress:\n%s", view)
		}
		m.logView = true
		if !strings.Contains(m.View(), "Esc Close") || lipgloss.Height(m.View()) > 24 {
			t.Fatal("compact stage clipped log panel controls")
		}
	}
}

// planAndApplyFixture starts a minimum-size review with one replacement awaiting approval.
func planAndApplyFixture() planAndApplyModel {
	m := planAndApplyModel{opts: PlanAndApplyOptions{Title: "Podplane · Create cluster", Subtitle: "demo · AWS · ap-southeast-2"}, width: 80, height: 24, stage: "Review", started: time.Now(), rows: map[string]*planAndApplyRow{}}
	m.accept(planAndApplyReview{plan: tfexec.Plan{HasChanges: true, Changes: []tfexec.Change{{Address: "module.cluster.aws_instance.server", Actions: []string{"delete", "create"}, Details: "Operation: delete → create\n\nBefore\nold\n\nAfter\nnew"}}}, answer: make(chan bool, 1)})
	return m
}
