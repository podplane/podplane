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

// OIDC configures where Podplane deploys a Truster server and how Truster
// runs there.
type OIDC struct {
	Provider Provider `json:"provider"`
	Hostname string   `json:"hostname"`
	Domain   Domain   `json:"domain"`
	// Truster is Truster's configuration, passed unchanged to the Truster
	// module as truster_config. Podplane sets the deployment-owned settings
	// (issuer_url, http_listen_addr, serving_certificate, secrets.provider)
	// itself, so they must not appear here.
	Truster map[string]any `json:"truster"`
}

// Provider identifies the cloud and account settings used to provision the OIDC server.
type Provider struct {
	Kind    string `json:"kind"`
	Region  string `json:"region,omitempty"`
	Account string `json:"account,omitempty"`
	Profile string `json:"profile,omitempty"`
	Project string `json:"project,omitempty"`
	Zone    string `json:"zone,omitempty"`
}

// Domain specifies the DNS zone and provider for the server hostname.
type Domain struct {
	Zone     string         `json:"zone,omitempty"`
	Provider DomainProvider `json:"provider,omitempty"`
}

// DomainProvider selects the DNS backend and the hosted zone holding the
// OIDC records: an optional Route 53 hosted zone ID for AWS, or the Cloud DNS
// managed zone name for Google Cloud.
type DomainProvider struct {
	Kind           string `json:"kind,omitempty"`
	HostedZoneID   string `json:"hosted_zone_id,omitempty"`
	HostedZoneName string `json:"hosted_zone_name,omitempty"`
}

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

// HasClient reports whether the Truster static policy has a client with
// clientID. Clients from a database policy are not visible here.
func (c *Config) HasClient(clientID string) bool {
	policy, _ := c.OIDC.Truster["static_policy"].(map[string]any)
	clients, _ := policy["clients"].(map[string]any)
	_, ok := clients[clientID]
	return ok
}
