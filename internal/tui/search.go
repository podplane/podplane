// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// textSearch searches visible rows or wrapped lines without filtering their contents.
type textSearch struct {
	input            textinput.Model
	query            string
	pattern          *regexp.Regexp
	editing, missing bool
	position         int
}

// update consumes search keys and returns the matching row, or -1 when unchanged.
// Cancellation and mode switching remain owned by the enclosing component.
func (s *textSearch) update(key tea.KeyMsg, lines []string, current int) (int, bool, tea.Cmd) {
	if key.String() == "ctrl+c" || key.String() == "ctrl+o" {
		return -1, false, nil
	}
	direction, start := 1, current
	if s.editing {
		switch key.String() {
		case "esc":
			s.editing = false
			return -1, true, nil
		case "enter":
			s.editing = false
			if value := s.input.Value(); value != "" {
				s.query = value
				s.pattern = regexp.MustCompile("(?i)" + regexp.QuoteMeta(value))
			}
			start-- // Include the current line for a new search.
		default:
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(key)
			return -1, true, cmd
		}
	} else {
		switch key.String() {
		case "/":
			s.input = textinput.New()
			s.input.Prompt = "/"
			s.input.CharLimit = 256
			s.editing = true
			return -1, true, s.input.Focus()
		case "n", "N":
			if key.String() == "N" {
				direction = -1
			}
		default:
			return -1, false, nil
		}
	}
	if s.pattern == nil {
		return -1, true, nil
	}
	for step := 1; step <= len(lines); step++ {
		i := ((start+direction*step)%len(lines) + len(lines)) % len(lines)
		if s.pattern.MatchString(ansi.Strip(lines[i])) {
			s.position, s.missing = i, false
			return i, true, nil
		}
	}
	s.missing = true
	return -1, true, nil
}

// view shows an editable prompt or the current search result in one existing footer row.
func (s textSearch) view(width int) string {
	if s.editing {
		s.input.Width = max(1, width-30)
		return s.input.View() + Shortcuts("  `Enter` Find · `Esc` Cancel")
	}
	if s.query == "" {
		return Shortcuts("`/` Search · `n/N` Next/previous")
	}
	status := Shortcuts(" · `n/N` Next/previous")
	if s.missing {
		status = " · No matches"
	}
	return ansi.Truncate("/"+s.query, max(1, width-ansi.StringWidth(status)), "…") + status
}

// highlight accents literal matches while preserving the row style across resets.
func (s textSearch) highlight(line string, base lipgloss.Style) string {
	line = base.Render(line)
	if s.pattern == nil {
		return line
	}
	plain := ansi.Strip(line)
	var b strings.Builder
	last := 0
	style := base.UnsetWidth().Inline(true).Bold(true).Underline(true).Foreground(ColorSecondary)
	for _, match := range s.pattern.FindAllStringIndex(plain, -1) {
		from, to := ansi.StringWidth(plain[:match[0]]), ansi.StringWidth(plain[:match[1]])
		b.WriteString(ansi.Cut(line, last, from))
		b.WriteString(style.Render(plain[match[0]:match[1]]))
		last = to
	}
	b.WriteString(ansi.Cut(line, last, ansi.StringWidth(line)))
	return b.String()
}
