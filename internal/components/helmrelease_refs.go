// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import "github.com/podplane/podplane/internal/health"

// HelmReleaseRefs builds runtime HelmRelease references for every app and CRD
// in an enable plan.
func (c *Config) HelmReleaseRefs(plan EnableSet) []health.HelmReleaseRef {
	items := make([]health.HelmReleaseRef, 0, len(plan.CRDs)+len(plan.Apps))
	for _, name := range plan.CRDs {
		items = append(items, health.HelmReleaseRef{Name: name, Namespace: "platform-cluster", Kind: "crd"})
	}
	for _, name := range plan.Apps {
		entry, _, _ := c.Get(name)
		ns := entry.Namespace
		if ns == "" {
			ns = HelmReleaseNamespace
		}
		items = append(items, health.HelmReleaseRef{Name: name, Namespace: ns, Kind: "app"})
	}
	return items
}
