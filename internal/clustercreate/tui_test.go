// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/pkg/seeds"
)

func TestCloudProviderItemsDefaultToAWSAndIncludeCancel(t *testing.T) {
	items := cloudProviderItems()
	if len(items) != 3 {
		t.Fatalf("cloud provider item count = %d, want 3", len(items))
	}

	aws, ok := items[0].(tui.Item)
	if !ok {
		t.Fatalf("first cloud provider item has type %T, want tui.Item", items[0])
	}
	if aws.Key != "aws" || aws.Label != "Amazon Web Services (AWS)" || aws.Cancel {
		t.Fatalf("first cloud provider item = %#v, want default AWS option", aws)
	}

	google := items[1].(tui.Item)
	if google.Key != "google" || google.Label != "Google Cloud" || google.Cancel {
		t.Fatalf("second cloud provider item = %#v, want Google Cloud option", google)
	}

	cancel := items[2].(tui.Item)
	if cancel.Label != "Cancel" || !cancel.Cancel {
		t.Fatalf("third cloud provider item = %#v, want cancel option", cancel)
	}
}

func TestNewConfigFormSkipsOIDCIssuerFieldWhenProvided(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")

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
	form := newConfigForm("", "v1.2.3-1")

	for _, field := range form.fields {
		if field.label == "OIDC issuer URL" {
			return
		}
	}
	t.Fatal("OIDC issuer URL field should be shown when issuer URL is not resolved")
}

// TestNewConfigFormRequiresIntentionalDomainlessAPIHostname verifies the API
// endpoint prompt has no inferred placeholder.
func TestNewConfigFormRequiresIntentionalDomainlessAPIHostname(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
	field := form.fields[indexForField(t, form, "Kubernetes API hostname")]
	if field.value != "" {
		t.Fatalf("Kubernetes API hostname default = %q, want empty", field.value)
	}
}

func TestNewConfigFormDefaultsToRecommendedSeed(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
	seedField := form.fields[indexForField(t, form, "Initial platform components (recommended, minimal, none)")]
	if seedField.value != seeds.Recommended {
		t.Fatalf("seed field default = %q, want %q", seedField.value, seeds.Recommended)
	}

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	if got := cfg.Cluster.Seed.Name; got != seeds.Recommended {
		t.Fatalf("cluster seed name = %q, want %q", got, seeds.Recommended)
	}
	if got := cfg.Cluster.Seed.Version; got != "v1.2.3-1" {
		t.Fatalf("cluster seed version = %q, want v1.2.3-1", got)
	}
}

func TestConfigFormCanSelectBareSeed(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
	form.fields[indexForField(t, form, "Initial platform components (recommended, minimal, none)")].value = seeds.None

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	if got := cfg.Cluster.Seed.Name; got != "" {
		t.Fatalf("cluster seed name = %q, want empty for bare seed", got)
	}
	if got := cfg.Cluster.Seed.Version; got != "" {
		t.Fatalf("cluster seed version = %q, want empty for bare seed", got)
	}
}

// TestConfigFormBuildsManagedDomainDefaults verifies the concise domain flow.
func TestConfigFormBuildsManagedDomainDefaults(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
	form.fields[indexForField(t, form, "Cluster domain (optional)")].value = "staging.example.com"
	form.fields[indexForField(t, form, "DNS provider (aws-route53 or blank for manual)")].value = "aws-route53"
	form.fields[indexForField(t, form, "ACME account email (optional)")].value = "ops@example.com"

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	if got, want := cfg.Cluster.Kubernetes.APIHostname, "k8s.staging.example.com"; got != want {
		t.Fatalf("API hostname = %q, want %q", got, want)
	}
	if got, want := cfg.Cluster.Registry.Hostname, "registry.staging.example.com"; got != want {
		t.Fatalf("registry hostname = %q, want %q", got, want)
	}
	listeners := cfg.Cluster.Providers[0].LoadBalancers["main"].Listeners
	if cfg.Cluster.Kubernetes.APILoadBalancer != "main" || len(listeners) != 2 || listeners[0].Pool != "control-plane" || listeners[1].Pool != "control-plane" {
		t.Fatalf("managed load balancer defaults = %#v, %#v", cfg.Cluster.Kubernetes, cfg.Cluster.Providers[0].LoadBalancers)
	}
	if cfg.Cluster.Domains[0].Provider == nil || cfg.Cluster.Domains[0].Provider.Kind != "aws-route53" {
		t.Fatalf("domain provider = %#v, want aws-route53", cfg.Cluster.Domains[0].Provider)
	}
	if cfg.Cluster.ACME == nil || cfg.Cluster.ACME.Email != "ops@example.com" || cfg.Cluster.ACME.Server != "" {
		t.Fatalf("ACME config = %#v, want email with default server", cfg.Cluster.ACME)
	}
}

// TestConfigFormOnlyShowsACMEEmailForSupportedDNS verifies that the ACME email
// field is visible only after selecting a DNS provider with ACME support.
func TestConfigFormOnlyShowsACMEEmailForSupportedDNS(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
	field := form.fields[indexForField(t, form, "ACME account email (optional)")]
	form.domain = "example.com"
	if form.fieldVisible(field) {
		t.Fatal("ACME email shown before a supported DNS provider is selected")
	}
	form.dnsProvider = "aws-route53"
	if !form.fieldVisible(field) {
		t.Fatal("ACME email hidden for a supported DNS provider")
	}
}

// TestConfigFormBuildsDomainlessCluster verifies explicit unmanaged API setup.
func TestConfigFormBuildsDomainlessCluster(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
	form.fields[indexForField(t, form, "Cluster ID / slug")].value = "production"
	form.fields[indexForField(t, form, "Kubernetes API hostname")].value = "api.example.net"

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	if got, want := cfg.Cluster.Kubernetes.APIHostname, "api.example.net"; got != want {
		t.Fatalf("API hostname = %q, want %q", got, want)
	}
	if got, want := cfg.Cluster.Registry.Hostname, "production-registry.local"; got != want {
		t.Fatalf("registry hostname = %q, want %q", got, want)
	}
	if len(cfg.Cluster.Domains) != 0 || len(cfg.Cluster.Providers[0].LoadBalancers) != 0 || cfg.Cluster.Kubernetes.APILoadBalancer != "" {
		t.Fatalf("domainless managed wiring = domains %#v, load balancers %#v, API load balancer %q", cfg.Cluster.Domains, cfg.Cluster.Providers[0].LoadBalancers, cfg.Cluster.Kubernetes.APILoadBalancer)
	}
}

func TestConfigFormCanNavigateBackWithoutLosingAnswers(t *testing.T) {
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
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
	form := newConfigForm("https://auth.example.com", "v1.2.3-1")
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
