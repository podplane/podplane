// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"fmt"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// tunnelForChoice maps the wizard selection to a tunnel; nil selects a cloud NLB.
func tunnelForChoice(choice string) *clusterconfig.Tunnel {
	switch choice {
	case "Cloudflare Tunnels":
		return &clusterconfig.Tunnel{Kind: "cloudflare", CredentialSecret: "tunnel-credentials"}
	default:
		return nil
	}
}

// ConfigureTunnel runs the selected provider's credentials setup.
func ConfigureTunnel(ctx context.Context, cfg *clusterconfig.ClusterConfig) error {
	if cfg.Cluster.Tunnel == nil {
		return nil
	}
	switch cfg.Cluster.Tunnel.Kind {
	case "cloudflare":
		return configureCloudflareTunnel(ctx, cfg)
	default:
		return fmt.Errorf("unsupported tunnel provider %q", cfg.Cluster.Tunnel.Kind)
	}
}
