// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidccreate

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConfigFormCanNavigateBackWithoutLosingAnswers(t *testing.T) {
	form := newConfigForm()
	form.input.SetValue("auth.example.com")
	if view := form.View(); strings.Contains(view, "shift+tab: back") {
		t.Fatalf("first OIDC step should not show back hint; view = %q", view)
	}

	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
	form = model.(configForm)
	if got := form.fields[0].value; got != "auth.example.com" {
		t.Fatalf("stored OIDC hostname = %q, want %q", got, "auth.example.com")
	}
	if got := form.fields[1].label; got != "DNS zone" {
		t.Fatalf("active field = %q, want DNS zone", got)
	}
	if view := form.View(); !strings.Contains(view, "shift+tab: back") {
		t.Fatalf("second OIDC step should show back hint; view = %q", view)
	}

	form.input.SetValue("example.com")
	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	if got := form.fields[1].value; got != "example.com" {
		t.Fatalf("stored DNS zone = %q, want %q", got, "example.com")
	}
	if got := form.fields[0].label; got != "OIDC hostname" {
		t.Fatalf("active field = %q, want OIDC hostname", got)
	}

	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyTab})
	form = model.(configForm)
	if got := form.input.Value(); got != "example.com" {
		t.Fatalf("restored DNS zone input = %q, want %q", got, "example.com")
	}
}
