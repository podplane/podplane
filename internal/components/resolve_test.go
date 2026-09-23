// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"reflect"
	"testing"
)

// fixture builds a small Config used across resolve tests. The shape mirrors
// the platform-components values.yaml: Envoy Gateway depends on its CRDs and
// the Gateway API CRDs.
func fixture() *Config {
	return &Config{
		Apps: map[string]Entry{
			"cilium":        {Enabled: true, Core: true, DependsOn: []string{"cilium-crds"}},
			"envoy-gateway": {Enabled: false, DependsOn: []string{"envoy-gateway-crds", "gateway-api-crds"}},
			"snapshot":      {Enabled: true, DependsOn: []string{"snapshot-crds"}},
		},
		CRDs: map[string]Entry{
			"cilium-crds":        {Enabled: true, Core: true},
			"envoy-gateway-crds": {Enabled: false},
			"gateway-api-crds":   {Enabled: true, Core: true},
			"snapshot-crds":      {Enabled: true},
		},
	}
}

// TestResolveEnableTransitive verifies component dependency resolution behavior.
func TestResolveEnableTransitive(t *testing.T) {
	got, err := fixture().ResolveEnable("envoy-gateway")
	if err != nil {
		t.Fatal(err)
	}
	wantApps := []string{"envoy-gateway"}
	wantCRDs := []string{"envoy-gateway-crds"}
	if !reflect.DeepEqual(got.Apps, wantApps) {
		t.Errorf("apps = %v, want %v", got.Apps, wantApps)
	}
	if !reflect.DeepEqual(got.CRDs, wantCRDs) {
		t.Errorf("crds = %v, want %v", got.CRDs, wantCRDs)
	}
}

// TestResolveEnableAlreadyEnabled verifies component dependency resolution behavior.
func TestResolveEnableAlreadyEnabled(t *testing.T) {
	got, err := fixture().ResolveEnable("snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsEmpty() {
		t.Errorf("expected empty set, got apps=%v crds=%v", got.Apps, got.CRDs)
	}
}

// TestResolveEnableUnknown verifies component dependency resolution behavior.
func TestResolveEnableUnknown(t *testing.T) {
	if _, err := fixture().ResolveEnable("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown component")
	}
}

// TestResolveEnableUnknownDependency verifies component dependency resolution behavior.
func TestResolveEnableUnknownDependency(t *testing.T) {
	cfg := &Config{
		Apps: map[string]Entry{
			"broken": {Enabled: false, DependsOn: []string{"missing"}},
		},
	}
	if _, err := cfg.ResolveEnable("broken"); err == nil {
		t.Fatal("expected error for unknown transitive dep")
	}
}

// TestEnabledDependents verifies component dependency resolution behavior.
func TestEnabledDependents(t *testing.T) {
	cfg := &Config{
		Apps: map[string]Entry{
			"envoy-gateway": {Enabled: true, DependsOn: []string{"envoy-gateway-crds"}},
		},
		CRDs: map[string]Entry{
			"envoy-gateway-crds": {Enabled: true},
		},
	}
	got := cfg.EnabledDependents("envoy-gateway-crds")
	want := []string{"envoy-gateway"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependents = %v, want %v", got, want)
	}
	if deps := cfg.EnabledDependents("envoy-gateway"); len(deps) != 0 {
		t.Errorf("envoy-gateway dependents = %v, want empty", deps)
	}
}
