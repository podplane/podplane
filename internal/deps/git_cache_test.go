// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import "testing"

func TestExactSemverTag(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantTag string
		wantOK  bool
	}{
		{name: "major minor", value: "1.2", wantTag: "1.2", wantOK: true},
		{name: "major minor patch", value: "1.2.3", wantTag: "1.2.3", wantOK: true},
		{name: "v prefix", value: "v1.2.3", wantTag: "v1.2.3", wantOK: true},
		{name: "trim whitespace", value: " v1.2.3 ", wantTag: "v1.2.3", wantOK: true},
		{name: "empty", value: "", wantOK: false},
		{name: "range", value: ">=1.0.0", wantOK: false},
		{name: "caret", value: "^1.2.3", wantOK: false},
		{name: "wildcard", value: "1.2.x", wantOK: false},
		{name: "prerelease", value: "1.2.3-beta.1", wantOK: false},
		{name: "major only", value: "1", wantOK: false},
		{name: "too many parts", value: "1.2.3.4", wantOK: false},
		{name: "empty part", value: "1..2", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTag, gotOK := exactSemverTag(tt.value)
			if gotOK != tt.wantOK {
				t.Fatalf("exactSemverTag(%q) ok = %v, want %v", tt.value, gotOK, tt.wantOK)
			}
			if gotTag != tt.wantTag {
				t.Fatalf("exactSemverTag(%q) tag = %q, want %q", tt.value, gotTag, tt.wantTag)
			}
		})
	}
}
