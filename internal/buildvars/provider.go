// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package buildvars

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var releaseVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+)(\.[0-9A-Za-z-]+)*)?$`)

// ProviderVersion returns the provider version for this build, failing closed on
// invalid linker-injected release versions rather than reporting an old version.
func ProviderVersion() string {
	version, err := ProviderVersionFor(BuildVersion())
	if err != nil {
		panic(err)
	}
	return version
}

// ProviderVersionFor maps a canonical CLI release version to the provider's
// monotonic version series. Build metadata is deliberately not accepted.
func ProviderVersionFor(version string) (string, error) {
	if version == "" || version == "dev" {
		return "0.0.0-dev", nil
	}
	m := releaseVersion.FindStringSubmatch(version)
	if m == nil {
		return "", fmt.Errorf("invalid release version %q", version)
	}
	parts := [3]uint64{}
	for i := range parts {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return "", fmt.Errorf("release version component overflow: %q", version)
		}
		parts[i] = n
	}
	if m[4] != "" {
		for _, id := range strings.Split(m[4][1:], ".") {
			if strings.Trim(id, "0123456789") == "" && len(id) > 1 && id[0] == '0' {
				return "", fmt.Errorf("noncanonical prerelease in %q", version)
			}
		}
	}
	switch parts[0] {
	case 0:
		if parts[1] >= 900 {
			return "", fmt.Errorf("CLI 0.x minor must be less than 900: %q", version)
		}
		parts[0], parts[1] = 1, 100+parts[1]
	case 1:
		if parts[1] > math.MaxUint64-1000 {
			return "", fmt.Errorf("provider minor overflow: %q", version)
		}
		parts[1] += 1000
	}
	return fmt.Sprintf("%d.%d.%d%s", parts[0], parts[1], parts[2], m[4]), nil
}
