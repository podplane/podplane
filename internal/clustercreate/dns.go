// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"

	"github.com/podplane/podplane/internal/cloud"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/tui"
)

// VerifyDNS checks existing configs before any deployment, using the same AWS
// credentials and exact zone-name or zone-ID lookup as generated Terraform.
func VerifyDNS(ctx context.Context, cfg *clusterconfig.ClusterConfig, unattended bool) error {
	provider := cfg.Cluster.Providers[0]
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider == nil || domain.Provider.Kind != "aws-route53" {
			continue
		}
		check := func(ctx context.Context) error {
			return cloud.CheckRoute53Zone(ctx, provider.Region, provider.Profile, domain.Zone, domain.Provider.HostedZoneID)
		}
		if err := tui.RunVerification(ctx, "Checking Route53 hosted zone", "Public hosted zone: "+domain.Zone, check, unattended); err != nil {
			return err
		}
	}
	return nil
}
