// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

import "fmt"

// loginConnectorNames maps the Truster login connector types a draft can set
// up to their display names. Other connectors, such as email, are configured
// by editing oidc.truster.user_login_connectors.
var loginConnectorNames = map[string]string{"google": "Google", "github": "GitHub"}

// NewDraftConfig returns a mutable draft config for providerKind with a
// connectorType login connector, rejecting empty or unsupported kinds. Its
// Truster configuration is the happy path: that one login connector and no
// clients yet. Each cluster that logs in through the server needs a client in
// static_policy.clients keyed by its OIDC client ID, which is its cluster ID
// unless cluster.oidc.client_id overrides it.
func NewDraftConfig(providerKind, connectorType string) (*Config, error) {
	displayName, ok := loginConnectorNames[connectorType]
	if !ok {
		return nil, fmt.Errorf("unsupported login connector %q; must be google or github", connectorType)
	}
	o := OIDC{Hostname: "auth.example.com", Domain: Domain{Zone: "example.com"}}
	var secret func(name string) string
	switch providerKind {
	case "google":
		o.Provider = Provider{Kind: "google", Project: "my-project", Region: "us-central1", Zone: "us-central1-a"}
		o.Domain.Provider = DomainProvider{Kind: "google", HostedZoneName: "example-com"}
		secret = func(name string) string { return "projects/my-project/secrets/" + name + "/versions/latest" }
	case "aws":
		o.Provider = Provider{Kind: "aws", Region: "us-east-1", Account: "123456789012", Profile: "default"}
		o.Domain.Provider = DomainProvider{Kind: "aws"}
		secret = func(name string) string { return "arn:aws:secretsmanager:us-east-1:123456789012:secret:" + name }
	default:
		return nil, fmt.Errorf("unsupported cloud provider %q; must be aws or google", providerKind)
	}
	o.Truster = map[string]any{
		"secrets": map[string]any{
			"signing_key_name":    secret("truster-signing-key"),
			"encryption_key_name": secret("truster-encryption-key"),
		},
		"user_login_connectors": map[string]any{
			connectorType: map[string]any{"type": connectorType, "display_name": displayName, "credentials_secret": secret("truster-" + connectorType)},
		},
		"static_policy": map[string]any{
			"default_redirect_uris": []any{"http://localhost:8000/callback"},
			"clients":               map[string]any{},
		},
	}
	return &Config{OIDC: o}, nil
}
