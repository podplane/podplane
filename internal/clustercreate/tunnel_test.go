// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"context"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// TestConfigureTunnel rejects unsupported providers rather than using Cloudflare's flow.
func TestConfigureTunnel(t *testing.T) {
	var cfg clusterconfig.ClusterConfig
	if err := ConfigureTunnel(context.Background(), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Cluster.Tunnel = &clusterconfig.Tunnel{Kind: "ngrok"}
	if err := ConfigureTunnel(context.Background(), &cfg); err == nil || !strings.Contains(err.Error(), `unsupported tunnel provider "ngrok"`) {
		t.Fatalf("unsupported provider: %v", err)
	}
}

// TestTunnelSelection restores NLB and DNS settings when a tunnel is deselected.
func TestTunnelSelection(t *testing.T) {
	m := testConfigForm(t, "aws", "v1.2.3-1")
	m.domain = "demo.example.com"
	m.fields[indexForField(t, m, "Cluster domain (optional)")].value = m.domain
	m.index = indexForField(t, m, "What do you want to use for Kubernetes API access and cluster ingress?")
	if m.fields[m.index].value != "Cloud load balancer (NLB)" {
		t.Fatal("cloud load balancer must be the default choice")
	}
	for _, choice := range []string{m.fields[m.index].value, "Cloudflare Tunnels", "Cloud load balancer (NLB)"} {
		m.input.SetValue(choice)
		m.storeCurrentValue()
		cfg, err := m.config()
		if err != nil {
			t.Fatal(err)
		}
		if choice == "Cloudflare Tunnels" {
			if cfg.Cluster.Tunnel == nil || cfg.Cluster.Tunnel.Kind != "cloudflare" || len(cfg.Cluster.Providers[0].LoadBalancers) != 0 {
				t.Fatal("Cloudflare selection must replace the NLB with a tunnel")
			}
			continue
		}
		if cfg.Cluster.Tunnel != nil || cfg.Cluster.Kubernetes.APILoadBalancer != "main" {
			t.Fatal("no tunnel must select the cloud NLB")
		}
		listeners := cfg.Cluster.Providers[0].LoadBalancers["main"].Listeners
		if len(listeners) != 2 || listeners[0].Port != 443 || listeners[1].Port != 6443 {
			t.Fatalf("NLB must serve application and Kubernetes API traffic: %v", listeners)
		}
		if !m.fieldVisible(m.fields[indexForField(t, m, "DNS provider")]) {
			t.Fatal("no tunnel must restore the DNS question")
		}
	}
}
