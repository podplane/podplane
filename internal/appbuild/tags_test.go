// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package appbuild

import "testing"

// TestNormalizeBuildTagAllowed verifies supported Podplane local image tags.
func TestNormalizeBuildTagAllowed(t *testing.T) {
	tests := []struct {
		in          string
		wantStore   string
		wantDisplay string
	}{
		{in: "api", wantStore: "apps/api:latest", wantDisplay: "default-registry.local/apps/api:latest"},
		{in: "api:v1", wantStore: "apps/api:v1", wantDisplay: "default-registry.local/apps/api:v1"},
		{in: "apps/api:v1", wantStore: "apps/api:v1", wantDisplay: "default-registry.local/apps/api:v1"},
		{in: "default-registry.local/apps/api:v1", wantStore: "apps/api:v1", wantDisplay: "default-registry.local/apps/api:v1"},
		{in: "other-registry.local/apps/api:v1", wantStore: "apps/api:v1", wantDisplay: "other-registry.local/apps/api:v1"},
	}
	for _, tt := range tests {
		store, display, err := NormalizeTags([]string{tt.in}, "default-registry.local")
		if err != nil {
			t.Fatalf("NormalizeTags(%q) error = %v", tt.in, err)
		}
		if store[0] != tt.wantStore || display[0] != tt.wantDisplay {
			t.Fatalf("NormalizeTags(%q) = (%q, %q), want (%q, %q)", tt.in, store[0], display[0], tt.wantStore, tt.wantDisplay)
		}
	}
}

// TestNormalizeBuildTagRejectsReservedAndRemoteRefs verifies unsupported refs fail clearly.
func TestNormalizeBuildTagRejectsReservedAndRemoteRefs(t *testing.T) {
	for _, in := range []string{
		"default-registry.local/mirror/api:v1",
		"default-registry.local/api:v1",
		"other-registry.local/mirror/api:v1",
		"mirror/api:v1",
		"ghcr.io/me/api:v1",
		"team/api:v1",
	} {
		if _, _, err := NormalizeTags([]string{in}, "default-registry.local"); err == nil {
			t.Fatalf("NormalizeTags(%q) succeeded, want error", in)
		}
	}
}
