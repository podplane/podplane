// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/podplane/podplane/internal/deps"
)

// TestDownloadProgressCompletion checks that a busy event stream cannot leave
// successfully completed transfers queued or active in the final display.
func TestDownloadProgressCompletion(t *testing.T) {
	m := downloadProgressModel{
		events: make(chan deps.DownloadEvent, 1),
		items:  map[string]downloadProgressItem{},
		run: func(emit func(deps.DownloadEvent)) error {
			for i := range 600 {
				name := fmt.Sprintf("artifact-%d", i)
				emit(deps.DownloadEvent{Type: deps.DownloadEventStarted, Name: name, Total: 10})
				emit(deps.DownloadEvent{Type: deps.DownloadEventProgress, Name: name, Current: 5, Total: 10})
				emit(deps.DownloadEvent{Type: deps.DownloadEventDone, Name: name, Current: 10, Total: 10})
			}
			return nil
		},
	}
	run := m.runCommand()
	go run()
	for event := range m.events {
		m.applyEvent(event)
	}
	if !strings.Contains(m.View(), "600 required, 0 active, 600 complete, 0 cached, 0 queued, 0 failed") {
		t.Fatalf("lost completion events:\n%s", m.View())
	}
}

// TestDownloadProgressNarrowTerminal keeps transfer bytes and the summary
// visible at the supported 80-column width, even with long artifact names.
func TestDownloadProgressNarrowTerminal(t *testing.T) {
	m := downloadProgressModel{
		title: "Preparing offline Podplane bundles", width: 80, height: 24,
		message: "Copying verified bundles to /a/very/long/destination/" + strings.Repeat("directory/", 10),
		items:   map[string]downloadProgressItem{},
	}
	for i := range 25 {
		m.applyEvent(deps.DownloadEvent{Type: deps.DownloadEventProgress,
			Name: fmt.Sprintf("qemu-system-bin-linux-amd64-x86_64-softmmu-11.0.0.%d.tar.gz", i), Current: 1 << 20, Total: 2 << 20})
	}
	view := m.View()
	for _, line := range strings.Split(strings.TrimSuffix(view, "\n"), "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Fatalf("line exceeds 80 columns: %q", line)
		}
	}
	if lines := strings.Count(view, "\n"); lines > 24 {
		t.Fatalf("view exceeds 24 rows: %d", lines)
	}
	if !strings.Contains(view, "1.0 MiB / 2.0 MiB") || !strings.Contains(view, "25 active") || !strings.Contains(view, "0 failed") {
		t.Fatalf("lost transfer bytes or summary:\n%s", view)
	}
	for i := range 25 {
		m.applyEvent(deps.DownloadEvent{Type: deps.DownloadEventDone,
			Name: fmt.Sprintf("qemu-system-bin-linux-amd64-x86_64-softmmu-11.0.0.%d.tar.gz", i), Total: 2 << 20})
	}
	if !strings.Contains(m.View(), "50.0 MiB / 50.0 MiB") {
		t.Fatalf("completion retained stale partial byte counts:\n%s", m.View())
	}
}
