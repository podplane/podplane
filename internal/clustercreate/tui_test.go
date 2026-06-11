// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewConfigFormSkipsOIDCIssuerFieldWhenProvided(t *testing.T) {
	form := newConfigForm("https://auth.example.com")

	for _, field := range form.fields {
		if field.label == "OIDC issuer URL" {
			t.Fatal("OIDC issuer URL field should be skipped when issuer URL is already resolved")
		}
	}

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	if got := cfg.Cluster.OIDC.IssuerURL; got != "https://auth.example.com" {
		t.Fatalf("cluster OIDC issuer URL = %q, want %q", got, "https://auth.example.com")
	}
}

func TestNewConfigFormPromptsOIDCIssuerFieldWhenMissing(t *testing.T) {
	form := newConfigForm("")

	for _, field := range form.fields {
		if field.label == "OIDC issuer URL" {
			return
		}
	}
	t.Fatal("OIDC issuer URL field should be shown when issuer URL is not resolved")
}

func TestConfigFormCanNavigateBackWithoutLosingAnswers(t *testing.T) {
	form := newConfigForm("https://auth.example.com")
	form.input.SetValue("production")
	if view := form.View(); strings.Contains(view, "shift+tab: back") {
		t.Fatalf("first cluster step should not show back hint; view = %q", view)
	}

	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
	form = model.(configForm)
	if got := form.fields[0].value; got != "production" {
		t.Fatalf("stored cluster name = %q, want %q", got, "production")
	}
	if got := form.fields[1].label; got != "Cluster ID / slug" {
		t.Fatalf("active field = %q, want Cluster ID / slug", got)
	}
	if view := form.View(); !strings.Contains(view, "shift+tab: back") {
		t.Fatalf("second cluster step should show back hint; view = %q", view)
	}

	form.input.SetValue("prod")
	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	if got := form.fields[1].value; got != "prod" {
		t.Fatalf("stored cluster ID = %q, want %q", got, "prod")
	}
	if got := form.fields[0].label; got != "Cluster name" {
		t.Fatalf("active field = %q, want Cluster name", got)
	}

	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyTab})
	form = model.(configForm)
	if got := form.input.Value(); got != "prod" {
		t.Fatalf("restored cluster ID input = %q, want %q", got, "prod")
	}
}

func TestConfigFormBackNavigationSkipsHiddenNetworkingFields(t *testing.T) {
	form := newConfigForm("https://auth.example.com")
	form.index = form.nextIndex(indexForField(t, form, "Control-plane architecture") - 1)
	form.input.SetValue(form.fields[form.index].value)

	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	if got := form.fields[form.index].label; got != "Configure networking options?" {
		t.Fatalf("active field = %q, want Configure networking options?", got)
	}
}

func indexForField(t *testing.T, form configForm, label string) int {
	t.Helper()
	for i, field := range form.fields {
		if field.label == label {
			return i
		}
	}
	t.Fatalf("field %q not found", label)
	return 0
}
