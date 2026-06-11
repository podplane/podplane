// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

var recommendedAddons = []string{"agent-sandbox", "cert-manager", "platform-certs", "traefik", "trust-manager"}

// RecommendedAddons returns addon components included by the recommended
// platform-components seed.
func RecommendedAddons() []string {
	return append([]string{}, recommendedAddons...)
}
