// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloud

// Provider is a cloud provider Podplane deploys to.
type Provider struct {
	// Kind is the provider's identifier in Podplane config files, such as
	// aws in cluster.providers[].kind or oidc.provider.kind.
	Kind string
	// Name is the provider's display name.
	Name string
}

// Providers returns the supported cloud providers in the order Podplane
// offers them.
func Providers() []Provider {
	return []Provider{
		{Kind: "aws", Name: "Amazon Web Services (AWS)"},
		{Kind: "google", Name: "Google Cloud"},
	}
}
