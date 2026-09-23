// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

var recommendedAddons = []string{
	"agent-sandbox",
	"envoy-gateway",
	"podplane-operator",
	"secrets-store-csi-driver",
	"zot-registry",
}

// RecommendedAddons returns addon components included by the recommended
// platform-components seed.
func RecommendedAddons() []string {
	return append([]string{}, recommendedAddons...)
}
