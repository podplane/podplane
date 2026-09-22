// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

// NewDraftConfig returns a mutable draft Truster config for the requested
// provider.
func NewDraftConfig(providerKind string) *Config {
	cfg := copyConfig(defaultDraftConfig)
	if providerKind == "" {
		providerKind = "aws"
	}
	cfg.OIDC.Provider = newDraftProvider(providerKind)
	cfg.OIDC.Domain.Provider.Kind = providerKind
	return &cfg
}

// newDraftProvider returns provider-specific draft deployment settings.
func newDraftProvider(kind string) Provider {
	switch kind {
	case "aws":
		return Provider{
			Kind:    "aws",
			Region:  "us-east-1",
			Account: "123456789012",
			Profile: "default",
		}
	default:
		return Provider{Kind: kind}
	}
}

var defaultDraftConfig = Config{OIDC: OIDC{
	Hostname: "auth.example.com",
	Domain: Domain{
		Zone: "example.com",
	},
	Connector: Connector{
		Kind:            "google",
		ClientSecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:truster-connector-secret",
	},
	SigningKeySecretARN:    "arn:aws:secretsmanager:us-east-1:123456789012:secret:truster-signing-key",
	EncryptionKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:truster-encryption-key",
	DefaultRedirectURIs:    []string{"http://localhost:8000"},
	Clients:                map[string]Client{"kubelogin": {}},
}}

// copyConfig returns a deep enough copy for wizard mutation.
func copyConfig(in Config) Config {
	out := in
	out.OIDC.DefaultRedirectURIs = append([]string(nil), in.OIDC.DefaultRedirectURIs...)
	out.OIDC.Clients = make(map[string]Client, len(in.OIDC.Clients))
	for name, client := range in.OIDC.Clients {
		client.RedirectURIs = append([]string(nil), client.RedirectURIs...)
		out.OIDC.Clients[name] = client
	}
	out.OIDC.GroupsOverrides = make(map[string]GroupsOverride, len(in.OIDC.GroupsOverrides))
	for name, override := range in.OIDC.GroupsOverrides {
		next := make(GroupsOverride, len(override))
		for user, groups := range override {
			next[user] = append([]string(nil), groups...)
		}
		out.OIDC.GroupsOverrides[name] = next
	}
	return out
}
