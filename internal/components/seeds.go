// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

var platformAddons = []string{
	"envoy-gateway",
	"podplane-operator",
	"secrets-store-csi-driver",
	"zot-registry",
}

// PlatformAddons returns addon components included by the platform
// platform-components seed.
func PlatformAddons() []string {
	return append([]string{}, platformAddons...)
}

// AgenticAddons returns the platform addons plus Agent Sandbox.
func AgenticAddons() []string {
	return append(PlatformAddons(), "agent-sandbox")
}
