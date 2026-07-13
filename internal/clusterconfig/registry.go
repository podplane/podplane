// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import "strings"

// CleanRegistryMirrorPrefix cleans a registry mirror path prefix.
func CleanRegistryMirrorPrefix(prefix string) string {
	return strings.Trim(strings.TrimSpace(prefix), "/")
}

// RegistryMirrorHostname returns the mirror hostname, defaulting to cluster.registry.hostname.
func (c Cluster) RegistryMirrorHostname() string {
	if c.Components.Registry != nil && c.Components.Registry.Mirror.Hostname != "" {
		return c.Components.Registry.Mirror.Hostname
	}
	if c.Registry.Hostname != "" {
		return c.Registry.Hostname
	}
	if len(c.Domains) == 0 {
		return c.ID + "-registry.local"
	}
	return ""
}

// RegistryMirrorPrefix returns the cleaned mirror prefix, defaulting to mirror.
func (c Cluster) RegistryMirrorPrefix() string {
	if c.Components.Registry != nil && c.Components.Registry.Mirror.Prefix != "" {
		return CleanRegistryMirrorPrefix(c.Components.Registry.Mirror.Prefix)
	}
	return "mirror"
}
