// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidccreate

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/podplane/podplane/internal/oidcconfig"
)

// TestLongInputWrapsWithoutChangingValue checks the whole value stays visible
// while resizing and editing across a visual line boundary.
func TestLongInputWrapsWithoutChangingValue(t *testing.T) {
	value := "arn:aws:secretsmanager:us-east-1:123456789012:secret:" + strings.Repeat("production-", 12) + "truster-google"
	for _, width := range []int{80, 40} {
		form := testConfigForm(t, "aws", "google", "prod-1")
		for i, field := range form.fields {
			if field.label == "Google OAuth client credentials secret ARN" {
				form.index = i
			}
		}
		form.input.SetValue(value)
		model, _ := form.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		form = model.(configForm)
		view := form.View()
		if lipgloss.Width(view) >= width || lipgloss.Height(view) > 24 || !strings.Contains(strings.ReplaceAll(ansi.Strip(view), "\n", ""), value) {
			t.Fatalf("input hidden or outside %dx24:\n%s", width, view)
		}
		form.input.SetCursor(width)
		model, _ = form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
		form = model.(configForm)
		want := value[:width] + "X" + value[width:]
		if form.input.Value() != want {
			t.Fatalf("wrapped edit = %q, want %q", form.input.Value(), want)
		}
		model, _ = form.Update(tea.KeyMsg{Type: tea.KeyEnter})
		form = model.(configForm)
		model, _ = form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		form = model.(configForm)
		if form.input.Value() != want || strings.Contains(form.input.Value(), "\n") {
			t.Fatalf("navigation changed wrapped value: %q", form.input.Value())
		}
	}
}

// TestConfigReusesClusterProvider exercises the shortened wizard and verifies
// inherited settings, including an intentionally empty AWS profile, survive.
func TestConfigReusesClusterProvider(t *testing.T) {
	for _, provider := range []oidcconfig.Provider{
		{Kind: "aws", Region: "eu-west-2", Account: "987654321098", Profile: "production"},
		{Kind: "aws", Region: "eu-west-2", Account: "987654321098"},
		{Kind: "google", Project: "production-project", Region: "europe-west2", Zone: "europe-west2-c"},
	} {
		form, err := newConfigForm(provider.Kind, "github", "prod-1", &provider)
		if err != nil {
			t.Fatal(err)
		}
		for steps := 0; !form.complete && steps < 20; steps++ {
			label := form.fields[form.index].label
			if strings.HasPrefix(label, "AWS ") || strings.HasPrefix(label, "Google Cloud ") {
				t.Fatalf("repeated provider question: %s", label)
			}
			if label == "Initial administrator email" {
				form.input.SetValue("admin@example.com")
			}
			model, _ := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
			form = model.(configForm)
			if form.err != nil {
				t.Fatal(form.err)
			}
		}
		if !form.complete {
			t.Fatal("wizard did not complete")
		}
		cfg, err := form.config()
		if err != nil || cfg.OIDC.Provider != provider {
			t.Fatalf("inherited provider lost: %v, %v", cfg, err)
		}
		prefix := "arn:aws:secretsmanager:eu-west-2:987654321098:secret:"
		if provider.Kind == "google" {
			prefix = "projects/production-project/secrets/"
		}
		for _, value := range cfg.OIDC.Truster["secrets"].(map[string]any) {
			if !strings.HasPrefix(value.(string), prefix) {
				t.Fatalf("secret reference uses another account/project: %s", value)
			}
		}
		standalone := testConfigForm(t, provider.Kind, "github", "prod-1")
		found := false
		for _, field := range standalone.fields {
			found = found || field.label == "AWS profile" || field.label == "Google Cloud project ID"
		}
		if !found {
			t.Fatal("standalone wizard lost provider questions")
		}
	}
}

// TestConfigFormCanNavigateBackWithoutLosingAnswers checks saved OIDC answers and back-navigation hints.
func TestConfigFormCanNavigateBackWithoutLosingAnswers(t *testing.T) {
	form := testConfigForm(t, "aws", "google", "")
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

// TestConfigRegistersClient verifies standalone and cluster-managed clients.
func TestConfigRegistersClient(t *testing.T) {
	for _, clientID := range []string{"", "custom-client"} {
		form := testConfigForm(t, "aws", "google", clientID)
		asked := false
		for i, field := range form.fields {
			if field.label == "First cluster ID" {
				asked = true
				for _, invalid := range []string{"", "Invalid ID"} {
					form.fields[i].value = invalid
					if _, err := form.config(); err == nil {
						t.Fatalf("accepted invalid cluster ID %q", invalid)
					}
				}
				form.fields[i].value = "prod-1"
			}
		}
		if asked != (clientID == "") {
			t.Fatalf("asked for cluster ID = %v with supplied client %q", asked, clientID)
		}
		cfg, err := form.config()
		if err != nil {
			t.Fatalf("config(%q) returned error: %v", clientID, err)
		}
		want := clientID
		if want == "" {
			want = "prod-1"
		}
		policy := cfg.OIDC.Truster["static_policy"].(map[string]any)
		clients := policy["clients"].(map[string]any)
		if len(clients) != 1 || !cfg.HasClient(want) {
			t.Fatalf("clients = %v, want only %q", clients, want)
		}
		client := clients[want].(map[string]any)
		mappings := policy["user_group_mappings"].(map[string]any)
		mapping := mappings[client["user_group_mapping"].(string)].(map[string]any)
		groups := mapping["admin@example.com"].([]any)
		if len(mappings) != 1 || len(mapping) != 1 || len(groups) != 1 || groups[0] != "podplane:admins" {
			t.Fatalf("initial administrator mapping = %v", mappings)
		}
		if policy["require_user_groups_from_policy"] == false || client["require_user_groups_from_policy"] == false {
			t.Fatal("wizard disabled the default deny policy for unmapped users")
		}
		redirects := policy["default_redirect_uris"].([]any)
		if len(redirects) != 1 || redirects[0] != "http://localhost:8000/callback" {
			t.Fatalf("redirects = %v", redirects)
		}
	}
}

// TestConfigUsesSelectedLoginConnector checks that the connector chosen
// before the form is the only one configured, with the credentials entered
// in the form.
func TestConfigUsesSelectedLoginConnector(t *testing.T) {
	form := testConfigForm(t, "google", "github", "prod-1")
	const credentials = "projects/acme/secrets/github-oauth/versions/3"
	for i, field := range form.fields {
		if field.label == "GitHub OAuth client credentials Secret Manager version" {
			form.fields[i].value = credentials
		}
	}
	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	connectors := cfg.OIDC.Truster["user_login_connectors"].(map[string]any)
	github, _ := connectors["github"].(map[string]any)
	if len(connectors) != 1 || github["type"] != "github" || github["credentials_secret"] != credentials {
		t.Fatalf("user_login_connectors = %v, want only github with the entered credentials", connectors)
	}
}

// testConfigForm constructs a wizard form or fails the calling test.
func testConfigForm(t *testing.T, providerKind, connector, clientID string) configForm {
	t.Helper()
	form, err := newConfigForm(providerKind, connector, clientID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range form.fields {
		if form.fields[i].label == "Initial administrator email" {
			form.fields[i].value = " Admin@Example.com "
		}
	}
	return form
}

// TestAdministratorEmailRequiresSingleMailbox checks the prompt rejects
// missing, malformed, display-name, and wildcard identities.
func TestAdministratorEmailRequiresSingleMailbox(t *testing.T) {
	for _, value := range []string{"", "not-an-email", "Admin <admin@example.com>", "*@example.com"} {
		form := testConfigForm(t, "google", "github", "prod-1")
		for i := range form.fields {
			if form.fields[i].label == "Initial administrator email" {
				form.index = i
			}
		}
		form.input.SetValue(value)
		model, _ := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got := model.(configForm)
		if got.index != form.index || got.err == nil {
			t.Fatalf("accepted administrator email %q", value)
		}
	}
}
