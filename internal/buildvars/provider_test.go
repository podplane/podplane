// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package buildvars

import "testing"

func TestProviderVersionFor(t *testing.T) {
	for input, want := range map[string]string{
		"": "0.0.0-dev", "dev": "0.0.0-dev", "v0.0.0": "1.100.0",
		"0.899.7": "1.999.7", "v1.0.0": "1.1000.0", "1.23.4-rc.1": "1.1023.4-rc.1",
		"v2.0.0": "2.0.0", "3.4.5-alpha-1.0": "3.4.5-alpha-1.0",
		"1.18446744073709550615.0": "1.18446744073709551615.0",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := ProviderVersionFor(input)
			if err != nil || got != want {
				t.Fatalf("ProviderVersionFor(%q) = %q, %v; want %q", input, got, err, want)
			}
		})
	}
	for _, input := range []string{"v", "DEV", " v1.2.3", "v01.2.3", "1.02.3", "1.2.03", "1.2", "1.2.3+sha", "1.2.3-01", "1.2.3-a..b", "0.900.0", "1.18446744073709550616.0", "18446744073709551616.0.0", "2.0.18446744073709551616"} {
		if got, err := ProviderVersionFor(input); err == nil {
			t.Errorf("ProviderVersionFor(%q) = %q without error", input, got)
		}
	}
}

func TestProviderVersionUsesBuildVersion(t *testing.T) {
	old := buildVersion
	t.Cleanup(func() { buildVersion = old })
	buildVersion = "v0.5.9-rc.2"
	if got := ProviderVersion(); got != "1.105.9-rc.2" {
		t.Fatal(got)
	}
	buildVersion = "not-a-release"
	defer func() {
		if recover() == nil {
			t.Fatal("invalid runtime version did not panic")
		}
	}()
	ProviderVersion()
}
