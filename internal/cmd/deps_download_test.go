// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"reflect"
	"testing"

	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/deps"
)

// TestInferredSecretsProvidersFromInfraProviders verifies infra providers imply secrets provider downloads.
func TestInferredSecretsProvidersFromInfraProviders(t *testing.T) {
	got := inferredSecretsProviders([]string{"aws", "gcp,proxmox"})
	want := []string{"aws", "gcp", "openbao"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inferredSecretsProviders() = %v, want %v", got, want)
	}
}

// TestDownloadAgenticBaseline verifies the default includes Agent Sandbox
// while the existing addon selector can include extra components.
func TestDownloadAgenticBaseline(t *testing.T) {
	manifest := deps.ComponentsManifest{Components: deps.Components{Images: []deps.ComponentImage{
		{Components: []string{"cilium"}, Image: "cilium"},
		{Components: []string{"envoy-gateway"}, Image: "envoy-gateway", Addon: true},
		{Components: []string{"agent-sandbox"}, Image: "agent-sandbox", Addon: true},
		{Components: []string{"snapshot"}, Image: "snapshot", Addon: true},
	}}}
	for _, tc := range []struct {
		name   string
		addons []string
		want   []int
	}{
		{"platform", components.PlatformAddons(), []int{0, 1}},
		{"default-agentic", components.AgenticAddons(), []int{0, 1, 2}},
		{"explicit-snapshot", append(components.AgenticAddons(), "snapshot"), []int{0, 1, 2, 3}},
		{"all", append(components.AgenticAddons(), "all"), []int{0, 1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := manifest.DownloadImageIndexes(deps.ComponentImageFilter{Addons: tc.addons})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("download image indexes = %v, want %v", got, tc.want)
			}
		})
	}
}
