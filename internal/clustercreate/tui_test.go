// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/podplane/podplane/pkg/seeds"
)

// TestConfigFormDerivesClusterIDFromName checks that the cluster ID follows
// the cluster name as a slug until the user edits it.
func TestConfigFormDerivesClusterIDFromName(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	idIndex := indexForField(t, form, "Cluster ID / slug")
	enter := func(value string) {
		t.Helper()
		form.input.SetValue(value)
		model, _ := form.Update(tea.KeyMsg{Type: tea.KeyEnter})
		form = model.(configForm)
	}

	enter("Prod EU 1")
	if got := form.fields[idIndex].value; got != "prod-eu-1" {
		t.Fatalf("cluster ID after name = %q, want prod-eu-1", got)
	}
	enter("billing")
	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	enter("Prod EU 2")
	if got := form.fields[idIndex].value; got != "billing" {
		t.Fatalf("edited cluster ID after renaming = %q, want billing kept", got)
	}
}

// TestClusterIDFromName checks slugs of names that need hyphens collapsed,
// trimmed, or truncated, and that the result passes cluster ID validation.
func TestClusterIDFromName(t *testing.T) {
	for name, want := range map[string]string{
		"Example Cluster":  "example-cluster",
		"  Prod -- EU/1! ": "prod-eu-1",
		"Café Staging":     "caf-staging",
		"An extremely long cluster name for production": "an-extremely-long-cluster-name-f",
		"Thirty-one characters exactly-x a":             "thirty-one-characters-exactly-x",
	} {
		got := clusterIDFromName(name)
		if got != want {
			t.Errorf("clusterIDFromName(%q) = %q, want %q", name, got, want)
		}
		if err := validateClusterID(got); err != nil {
			t.Errorf("clusterIDFromName(%q) = %q, which is invalid: %v", name, got, err)
		}
	}
}

// TestNewConfigFormRequiresIntentionalDomainlessAPIHostname verifies the API
// endpoint prompt has no inferred placeholder.
func TestNewConfigFormRequiresIntentionalDomainlessAPIHostname(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	field := form.fields[indexForField(t, form, "Kubernetes API hostname")]
	if field.value != "" {
		t.Fatalf("Kubernetes API hostname default = %q, want empty", field.value)
	}
}

// TestNewConfigFormDefaultsToAgenticSeed checks the default seed name and resolved version.
func TestNewConfigFormDefaultsToAgenticSeed(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	seedField := form.fields[indexForField(t, form, "Cluster seed")]
	if seedField.value != seeds.Agentic {
		t.Fatalf("seed field default = %q, want %q", seedField.value, seeds.Agentic)
	}

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	if got := cfg.Cluster.Seed.Name; got != seeds.Agentic {
		t.Fatalf("cluster seed name = %q, want %q", got, seeds.Agentic)
	}
	if got := cfg.Cluster.Seed.Version; got != "v1.2.3-1" {
		t.Fatalf("cluster seed version = %q, want v1.2.3-1", got)
	}
}

// TestConfigFormCanSelectPlatformSeed checks the prompt-to-config mapping.
func TestConfigFormCanSelectPlatformSeed(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.index = indexForField(t, form, "Cluster seed")
	form.input.SetValue(form.fields[form.index].value)
	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyDown})
	form = model.(configForm)
	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyEnter})
	form = model.(configForm)
	cfg, err := form.config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster.Seed.Name != seeds.Platform || cfg.Cluster.Seed.Version != "v1.2.3-1" {
		t.Fatalf("platform seed = %#v", cfg.Cluster.Seed)
	}
	model, _ = form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	if form.input.Value() != seeds.Platform {
		t.Fatalf("restored seed = %q, want platform", form.input.Value())
	}
}

// TestConfigFormCanSelectBareSeed checks that selecting none clears seed name and version.
func TestConfigFormCanSelectBareSeed(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.index = indexForField(t, form, "Cluster seed")
	form.input.SetValue(form.fields[form.index].value)
	for _, want := range []string{seeds.Platform, seeds.Minimal, seeds.None} {
		model, _ := form.Update(tea.KeyMsg{Type: tea.KeyDown})
		form = model.(configForm)
		if form.input.Value() != want {
			t.Fatalf("selected seed = %q, want %q", form.input.Value(), want)
		}
	}
	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyTab})
	form = model.(configForm)

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

// TestDNSSelection verifies manual defaults, managed selection and backtracking
// produce the intended DNS configuration on both clouds.
func TestDNSSelection(t *testing.T) {
	for cloud, kind := range map[string]string{"aws": "aws-route53", "google": "google-cloud-dns"} {
		form := testConfigForm(t, cloud, "v1.2.3-1")
		form.fields[indexForField(t, form, "Cluster domain (optional)")].value = "example.com"
		form.domain = "example.com"
		if cloud == "google" {
			form.fields[indexForField(t, form, "Google Cloud project ID")].value = "test-project"
			form.fields[indexForField(t, form, "Google Cloud DNS managed-zone resource name")].value = "example-zone"
		}
		form.index = indexForField(t, form, "DNS provider")
		form.input.SetValue(form.fields[form.index].value)
		if form.input.Value() != "manual" {
			t.Fatal("manual DNS should remain the default")
		}
		press := func(key tea.KeyType) {
			model, _ := form.Update(tea.KeyMsg{Type: key})
			form = model.(configForm)
		}
		press(tea.KeyDown)
		press(tea.KeyEnter)
		cfg, err := form.config()
		if err != nil || cfg.Cluster.Domains[0].Provider == nil || cfg.Cluster.Domains[0].Provider.Kind != kind {
			t.Fatalf("managed DNS selection: %v, %v", cfg, err)
		}
		press(tea.KeyShiftTab)
		if form.input.Value() != kind {
			t.Fatal("back navigation lost DNS selection")
		}
		press(tea.KeyUp)
		press(tea.KeyEnter)
		cfg, err = form.config()
		if err != nil || cfg.Cluster.Domains[0].Provider != nil || cfg.Cluster.ACME != nil {
			t.Fatalf("manual DNS kept managed settings: %v, %v", cfg, err)
		}
	}
}

// TestDNSVerification checks selection stays editable after failure, while
// skipping preserves the selected DNS provider and ignores late results.
func TestDNSVerification(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.ctx = t.Context()
	form.domain = "test.example.com"
	form.fields[indexForField(t, form, "AWS profile (optional)")].value = "chosen"
	form.index = indexForField(t, form, "DNS provider")
	form.input.SetValue("aws-route53")
	failure := errors.New("hosted zone missing")
	form.verifyDNS = func(ctx context.Context, region, profile, name, id string) error {
		if profile != "chosen" || region != "us-east-1" || name != "test.example.com" || id != "" {
			t.Fatalf("incorrect lookup inputs: %s %s %s %s", region, profile, name, id)
		}
		return failure
	}
	update := func(msg tea.Msg) tea.Cmd {
		model, cmd := form.Update(msg)
		form = model.(configForm)
		return cmd
	}
	cmd := update(tea.KeyMsg{Type: tea.KeyEnter})
	// Execute the asynchronous check and feed its result through the wizard.
	batch := cmd().(tea.BatchMsg)
	result := batch[1]()
	update(result)
	update(tea.KeyMsg{Type: tea.KeyEnter})
	if form.fields[form.index].label != "DNS provider" || !errors.Is(form.err, failure) {
		t.Fatal("failure should return to the DNS selection for editing")
	}
	cmd = update(tea.KeyMsg{Type: tea.KeyEnter})
	batch = cmd().(tea.BatchMsg)
	update(tea.KeyMsg{Type: tea.KeyEsc})
	update(tea.KeyMsg{Type: tea.KeyEsc})
	update(batch[1]())
	if form.cancel || form.verification != nil || form.fields[form.index].label != "ACME account email (optional)" || form.dnsProvider != "aws-route53" {
		t.Fatal("skip, repeated Escape, or late result changed the DNS answer or cancelled creation")
	}
	update(tea.KeyMsg{Type: tea.KeyShiftTab})
	update(tea.KeyMsg{Type: tea.KeyUp})
	update(tea.KeyMsg{Type: tea.KeyEnter})
	if form.verification != nil || form.dnsProvider != "manual" {
		t.Fatal("manual DNS should not verify online")
	}
}

// TestConfigFormBuildsManagedDomainDefaults verifies the concise domain flow.
func TestConfigFormBuildsManagedDomainDefaults(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.fields[indexForField(t, form, "Cluster domain (optional)")].value = "staging.example.com"
	form.fields[indexForField(t, form, "DNS provider")].value = "aws-route53"
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
	form := testConfigForm(t, "aws", "v1.2.3-1")
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

// TestConfigFormBuildsDomainlessCluster keeps the selected NLB while leaving
// DNS management to the user when only an API hostname is supplied.
func TestConfigFormBuildsDomainlessCluster(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
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
	if len(cfg.Cluster.Domains) != 0 || len(cfg.Cluster.Providers[0].LoadBalancers) != 1 || cfg.Cluster.Kubernetes.APILoadBalancer != "main" {
		t.Fatalf("domainless managed wiring = domains %#v, load balancers %#v, API load balancer %q", cfg.Cluster.Domains, cfg.Cluster.Providers[0].LoadBalancers, cfg.Cluster.Kubernetes.APILoadBalancer)
	}
}

// TestConfigFormAdaptsSubnetsToCustomVPCAndRegion verifies generated subnet
// CIDRs and availability zones follow the wizard's networking inputs.
func TestConfigFormAdaptsSubnetsToCustomVPCAndRegion(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.showNetworking = true
	form.fields[indexForField(t, form, "AWS region")].value = "us-west-2"
	form.fields[indexForField(t, form, "VPC IPv4 CIDR")].value = "172.19.0.0/16"
	form.fields[indexForField(t, form, "AWS availability zone")].value = "us-west-2a"

	cfg, err := form.config()
	if err != nil {
		t.Fatalf("config returned error: %v", err)
	}
	provider := cfg.Cluster.Providers[0]
	if _, ok := provider.Zones["us-west-2a"]; !ok {
		t.Fatalf("provider zones = %#v, want us-west-2a", provider.Zones)
	}
	for _, subnet := range provider.Zones["us-west-2a"] {
		if !strings.HasPrefix(subnet.V4CIDR, "172.19.") {
			t.Fatalf("subnet CIDR = %q, want it within 172.19.0.0/16", subnet.V4CIDR)
		}
	}
}

// TestConfigFormRejectsAvailabilityZoneOutsideRegion preserves final config
// validation as a fallback for the immediate field validation.
func TestConfigFormRejectsAvailabilityZoneOutsideRegion(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.showNetworking = true
	form.fields[indexForField(t, form, "AWS region")].value = "us-west-2"
	form.fields[indexForField(t, form, "AWS availability zone")].value = "us-west-1a"

	if _, err := form.config(); err == nil || !strings.Contains(err.Error(), "must be an availability zone in region us-west-2") {
		t.Fatalf("config error = %v, want region mismatch error", err)
	}
}

// TestConfigFormRejectsInvalidNetworkFieldsBeforeAdvancing verifies each
// editable networking field keeps focus and displays its validation error.
func TestConfigFormRejectsInvalidNetworkFieldsBeforeAdvancing(t *testing.T) {
	tests := []struct {
		field   string
		value   string
		prepare func(*configForm)
		wantErr string
	}{
		{field: "AWS region", value: "us-west", wantErr: "valid AWS region"},
		{field: "VPC IPv4 CIDR", value: "172.19.0.1/16", wantErr: "canonical IPv4 /16"},
		{
			field: "AWS availability zone", value: "us-west-1a", wantErr: "availability zone in region us-west-2",
			prepare: func(form *configForm) {
				form.fields[indexForField(t, *form, "AWS region")].value = "us-west-2"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			form := testConfigForm(t, "aws", "v1.2.3-1")
			form.showNetworking = true
			if tt.prepare != nil {
				tt.prepare(&form)
			}
			form.index = indexForField(t, form, tt.field)
			form.input.SetValue(tt.value)

			model, _ := form.moveNext()
			form = model.(configForm)
			if form.err == nil || !strings.Contains(form.err.Error(), tt.wantErr) {
				t.Fatalf("field error = %v, want error containing %q", form.err, tt.wantErr)
			}
			if got := form.fields[form.index].label; got != tt.field {
				t.Fatalf("active field = %q, want %q", got, tt.field)
			}
		})
	}
}

// TestConfigFormCanNavigateBackWithoutLosingAnswers checks saved answers and back-navigation hints.
func TestConfigFormCanNavigateBackWithoutLosingAnswers(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.input.SetValue("production")
	if view := form.View(); strings.Contains(view, "Shift+Tab Back") {
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
	if view := form.View(); !strings.Contains(view, "Shift+Tab Back") {
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

// TestConfigFormBackNavigationSkipsHiddenNetworkingFields checks navigation past disabled network prompts.
func TestConfigFormBackNavigationSkipsHiddenNetworkingFields(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.index = form.nextIndex(indexForField(t, form, "Control plane architecture") - 1)
	form.input.SetValue(form.fields[form.index].value)

	model, _ := form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	form = model.(configForm)
	if got := form.fields[form.index].label; got != "Configure networking options?" {
		t.Fatalf("active field = %q, want Configure networking options?", got)
	}
}

// TestConfigFormArchitectureSelection checks default and alternate architectures,
// matching machine suggestions, and preservation of a custom machine type.
func TestConfigFormArchitectureSelection(t *testing.T) {
	for _, tt := range []struct{ provider, arch, machine, alternateArch, alternateMachine string }{
		{"aws", "arm64", "t4g.medium", "amd64", "t3.medium"},
		{"google", "amd64", "e2-medium", "arm64", "t2a-standard-2"},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			form := testConfigForm(t, tt.provider, "v1.2.3-1")
			if tt.provider == "google" {
				form.fields[indexForField(t, form, "Google Cloud project ID")].value = "test-project"
			}
			cfg, err := form.config()
			if err != nil {
				t.Fatal(err)
			}
			if pool := cfg.Cluster.Pools["control-plane"]; pool.Arch != tt.arch || pool.InstanceType != tt.machine {
				t.Fatalf("default pool = %#v", pool)
			}
			form.index = indexForField(t, form, "Control plane architecture")
			form.input.SetValue(form.fields[form.index].value)
			press := func(key tea.KeyMsg) {
				model, _ := form.Update(key)
				form = model.(configForm)
			}
			press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("invalid")})
			if form.input.Value() != tt.arch {
				t.Fatal("selector accepted free text")
			}
			press(tea.KeyMsg{Type: tea.KeyDown})
			press(tea.KeyMsg{Type: tea.KeyEnter})
			cfg, err = form.config()
			if err != nil {
				t.Fatal(err)
			}
			if pool := cfg.Cluster.Pools["control-plane"]; pool.Arch != tt.alternateArch || pool.InstanceType != tt.alternateMachine {
				t.Fatalf("selected pool = %#v", pool)
			}
			form.input.SetValue("custom-machine")
			press(tea.KeyMsg{Type: tea.KeyShiftTab})
			press(tea.KeyMsg{Type: tea.KeyUp})
			press(tea.KeyMsg{Type: tea.KeyEnter})
			if form.input.Value() != "custom-machine" {
				t.Fatalf("custom instance type overwritten: %q", form.input.Value())
			}
		})
	}
}

// TestConfigFormNetworkingSelection checks both yes/no branches and backtracking.
func TestConfigFormNetworkingSelection(t *testing.T) {
	form := testConfigForm(t, "aws", "v1.2.3-1")
	form.index = indexForField(t, form, "Configure networking options?")
	form.input.SetValue(form.fields[form.index].value)
	press := func(key tea.KeyType) {
		model, _ := form.Update(tea.KeyMsg{Type: key})
		form = model.(configForm)
	}
	press(tea.KeyEnter)
	if form.fields[form.index].label != "Control plane architecture" {
		t.Fatal("networking defaults should skip advanced fields")
	}
	press(tea.KeyShiftTab)
	press(tea.KeyUp)
	press(tea.KeyEnter)
	if form.fields[form.index].label != "IP mode" {
		t.Fatal("yes should show IP mode first")
	}
	press(tea.KeyEnter)
	if form.fields[form.index].label != "NAT" {
		t.Fatal("IP mode should be followed by NAT")
	}
	if form.input.Value() != "Cloud-provider NAT" {
		t.Fatalf("default NAT selection = %q", form.input.Value())
	}
	press(tea.KeyEnter)
	if form.fields[form.index].label != "VPC IPv4 CIDR" {
		t.Fatal("yes should show advanced networking")
	}
	form.input.SetValue("172.20.0.0/16")
	press(tea.KeyShiftTab)
	press(tea.KeyShiftTab)
	press(tea.KeyShiftTab)
	if form.input.Value() != "yes" {
		t.Fatal("back navigation lost yes selection")
	}
	press(tea.KeyDown)
	press(tea.KeyEnter)
	if form.fields[form.index].label != "Control plane architecture" {
		t.Fatal("no should hide advanced networking again")
	}
	press(tea.KeyShiftTab)
	press(tea.KeyUp)
	press(tea.KeyEnter)
	press(tea.KeyEnter)
	press(tea.KeyEnter)
	if form.input.Value() != "172.20.0.0/16" {
		t.Fatal("hidden networking answer was lost")
	}
}

// testConfigForm constructs a wizard form or fails the calling test.
func testConfigForm(t *testing.T, providerKind, seedVersion string) configForm {
	t.Helper()
	form, err := newConfigForm(providerKind, seedVersion)
	if err != nil {
		t.Fatal(err)
	}
	return form
}

// indexForField finds a form field by label or fails the test.
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
