// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package execwrap

import "testing"

// TestVersionCheck checks major-version requirements and malformed version handling.
func TestVersionCheck(t *testing.T) {
	tests := []struct {
		name         string
		version      string
		requireMajor int
		want         bool
		wantErr      bool
	}{
		{
			name:         "exact major version match",
			version:      "2.0.0",
			requireMajor: 2,
			want:         true,
			wantErr:      false,
		},
		{
			name:         "higher major version",
			version:      "3.1.4",
			requireMajor: 2,
			want:         true,
			wantErr:      false,
		},
		{
			name:         "lower major version",
			version:      "1.9.9",
			requireMajor: 2,
			want:         false,
			wantErr:      false,
		},
		{
			name:         "with v prefix",
			version:      "v4.0.0",
			requireMajor: 3,
			want:         true,
			wantErr:      false,
		},
		{
			name:         "invalid version format",
			version:      "invalid",
			requireMajor: 1,
			want:         false,
			wantErr:      true,
		},
		{
			name:         "incomplete version",
			version:      "2",
			requireMajor: 1,
			want:         false,
			wantErr:      true,
		},
		{
			name:         "version with additional metadata",
			version:      "2.0.0-beta.1",
			requireMajor: 2,
			want:         true,
			wantErr:      false,
		},
		{
			name:         "empty version string",
			version:      "",
			requireMajor: 1,
			want:         false,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := VersionCheck(tt.version, tt.requireMajor)
			if (err != nil) != tt.wantErr {
				t.Errorf("VersionCheck() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("VersionCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}
