// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"reflect"
	"testing"
)

// TestSeedAddons checks the image-prefetch baseline and agentic extension.
func TestSeedAddons(t *testing.T) {
	wantPlatform := []string{"envoy-gateway", "podplane-operator", "secrets-store-csi-driver", "zot-registry"}
	if got := PlatformAddons(); !reflect.DeepEqual(got, wantPlatform) {
		t.Fatalf("PlatformAddons() = %v, want %v", got, wantPlatform)
	}
	wantAgentic := append(append([]string{}, wantPlatform...), "agent-sandbox", "sourcegateway", "agent-router")
	if got := AgenticAddons(); !reflect.DeepEqual(got, wantAgentic) {
		t.Fatalf("AgenticAddons() = %v, want %v", got, wantAgentic)
	}
	PlatformAddons()[0] = "changed"
	AgenticAddons()[0] = "changed"
	if got := PlatformAddons(); !reflect.DeepEqual(got, wantPlatform) {
		t.Fatalf("caller mutated addon baseline: %v", got)
	}
}
