// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"slices"
	"strings"
)

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

// AgenticAddons returns the platform addons plus the agentic components.
func AgenticAddons() []string {
	return append(PlatformAddons(), "agent-sandbox", "sourcegateway", "agent-router")
}

// secretsCSIProviders are secrets provider kinds with a Secrets Store CSI
// provider addon named secrets-store-csi-provider-<kind>.
var secretsCSIProviders = []string{"aws", "gcp", "openbao", "vault"}

// DefaultAddons returns the "agentic" seed addons plus the Secrets Store CSI
// provider addon for each secrets provider in a comma-separated provider
// selection, so selecting a provider such as openbao also downloads the images
// seeds enable.
func DefaultAddons(providers []string) []string {
	addons := AgenticAddons()
	for _, value := range providers {
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			for _, kind := range secretsCSIProviders {
				addon := "secrets-store-csi-provider-" + kind
				if (name == kind || name == "all") && !slices.Contains(addons, addon) {
					addons = append(addons, addon)
				}
			}
		}
	}
	return addons
}
