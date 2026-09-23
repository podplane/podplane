// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"reflect"
	"testing"
)

func TestBuildEnabledPatch(t *testing.T) {
	got := buildEnabledPatch([]string{"cert-manager"}, []string{"cert-manager-crds"}, true)
	want := map[string]any{
		"spec": map[string]any{
			"values": map[string]any{
				"platform": map[string]any{
					"components": map[string]any{
						"apps": map[string]any{
							"cert-manager": map[string]any{"enabled": true},
						},
						"crds": map[string]any{
							"cert-manager-crds": map[string]any{"enabled": true},
						},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("patch mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestBuildEnabledPatchOnlyApps(t *testing.T) {
	got := buildEnabledPatch([]string{"envoy-gateway"}, nil, false)
	components := got["spec"].(map[string]any)["values"].(map[string]any)["platform"].(map[string]any)["components"].(map[string]any)
	if _, hasCRDs := components["crds"]; hasCRDs {
		t.Errorf("expected no crds key when no CRDs passed")
	}
}
