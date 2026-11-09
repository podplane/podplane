// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Shortcuts renders backtick-delimited keys in bold teal and descriptions in muted text.
// Mark only static hints, never user input or Inline output.
func Shortcuts(text string) string {
	key := lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)
	label := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#AAAAAA"})
	lines := strings.Split(text, "\n")
	for row, line := range lines {
		parts := strings.Split(line, "`")
		for i, part := range parts {
			if i%2 == 1 {
				parts[i] = key.Render(part)
			} else {
				parts[i] = label.Render(part)
			}
		}
		lines[row] = strings.Join(parts, "")
	}
	return strings.Join(lines, "\n")
}
