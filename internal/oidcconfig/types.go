// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

import (
	"net/url"
	"strings"
)

// Config is the user-authored OIDC server configuration with an optional schema reference.
type Config struct {
	Schema string `json:"$schema,omitempty"`
	OIDC   OIDC   `json:"oidc"`
}

// OIDC configures server infrastructure, upstream identity, signing secrets, and client access.
type OIDC struct {
	Provider               Provider                  `json:"provider"`
	Hostname               string                    `json:"hostname"`
	Domain                 Domain                    `json:"domain"`
	Connector              Connector                 `json:"connector"`
	SigningKeySecretARN    string                    `json:"signing_key_secret_arn"`
	EncryptionKeySecretARN string                    `json:"encryption_key_secret_arn,omitempty"`
	DefaultRedirectURIs    []string                  `json:"default_redirect_uris,omitempty"`
	Clients                map[string]Client         `json:"clients,omitempty"`
	GroupsOverrides        map[string]GroupsOverride `json:"groups_overrides,omitempty"`
}

// Provider identifies the cloud and account settings used to provision the OIDC server.
type Provider struct {
	Kind    string `json:"kind"`
	Region  string `json:"region,omitempty"`
	Account string `json:"account,omitempty"`
	Profile string `json:"profile,omitempty"`
	Project string `json:"project,omitempty"`
}

// Domain specifies the DNS zone and provider for the server hostname.
type Domain struct {
	Zone     string         `json:"zone,omitempty"`
	Provider DomainProvider `json:"provider,omitempty"`
}

// DomainProvider selects the DNS backend and optional hosted zone ID.
type DomainProvider struct {
	Kind         string `json:"kind,omitempty"`
	HostedZoneID string `json:"hosted_zone_id,omitempty"`
}

// Connector selects the upstream identity provider and its client secret.
type Connector struct {
	Kind            string `json:"kind"`
	ClientSecretARN string `json:"client_secret_arn"`
}

// Client configures redirect URIs and a named group override for an OIDC client.
type Client struct {
	GroupsOverride string   `json:"groups_override,omitempty"`
	RedirectURIs   []string `json:"redirect_uris,omitempty"`
}

// GroupsOverride maps user identities to replacement group memberships.
type GroupsOverride map[string][]string

// IssuerURL returns the HTTPS issuer URL for the configured OIDC hostname.
func (c *Config) IssuerURL() string {
	if c == nil || c.OIDC.Hostname == "" {
		return ""
	}
	if strings.Contains(c.OIDC.Hostname, "://") {
		u, err := url.Parse(c.OIDC.Hostname)
		if err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return "https://" + c.OIDC.Hostname
}
