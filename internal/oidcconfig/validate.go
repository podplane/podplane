// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

import (
	"fmt"
	"net/url"
	"strings"
)

// deploymentOwnedTrusterSettings are the Truster settings Podplane sets from
// the deployment, so a config must not set them in oidc.truster.
var deploymentOwnedTrusterSettings = []string{"issuer_url", "http_listen_addr", "serving_certificate"}

// Validate checks the deployment settings Podplane owns. The Truster
// configuration in oidc.truster is validated by the JSON Schema and by the
// Truster module, not here.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	o := cfg.OIDC
	switch o.Provider.Kind {
	case "aws":
		if o.Provider.Region == "" {
			return fmt.Errorf("oidc.provider.region is required for aws")
		}
	case "google":
		if o.Provider.Project == "" || o.Provider.Region == "" || o.Provider.Zone == "" {
			return fmt.Errorf("oidc.provider.project, region, and zone are required for google")
		}
		if !strings.HasPrefix(o.Provider.Zone, o.Provider.Region+"-") {
			return fmt.Errorf("oidc.provider.zone %q is not in region %q", o.Provider.Zone, o.Provider.Region)
		}
	case "":
		return fmt.Errorf("oidc.provider.kind is required")
	default:
		return fmt.Errorf("oidc.provider.kind %q is not supported", o.Provider.Kind)
	}
	if o.Hostname == "" {
		return fmt.Errorf("oidc.hostname is required")
	}
	if strings.Contains(o.Hostname, "://") {
		u, err := url.Parse(o.Hostname)
		if err != nil || u.Host == "" {
			return fmt.Errorf("oidc.hostname must be a hostname, not a URL")
		}
	}
	switch o.Domain.Provider.Kind {
	case "":
	case o.Provider.Kind:
		if o.Domain.Provider.Kind == "google" && o.Domain.Zone != "" && o.Domain.Provider.HostedZoneName == "" {
			return fmt.Errorf("oidc.domain.provider.hosted_zone_name is required for google managed DNS")
		}
	default:
		return fmt.Errorf("oidc.domain.provider.kind %q must match oidc.provider.kind %q for managed DNS", o.Domain.Provider.Kind, o.Provider.Kind)
	}
	if o.Truster == nil {
		return fmt.Errorf("oidc.truster is required")
	}
	for _, name := range deploymentOwnedTrusterSettings {
		if _, ok := o.Truster[name]; ok {
			return fmt.Errorf("oidc.truster.%s is set by Podplane from the deployment; remove it", name)
		}
	}
	if secrets, ok := o.Truster["secrets"].(map[string]any); ok {
		if _, ok := secrets["provider"]; ok {
			return fmt.Errorf("oidc.truster.secrets.provider is set by Podplane from oidc.provider; remove it")
		}
	}
	return nil
}
