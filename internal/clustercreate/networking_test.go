// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import (
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// TestWizardNetworking checks defaults and explicit selections on both clouds,
// including switching from managed DNS to a tunnel without keeping DNS settings.
func TestWizardNetworking(t *testing.T) {
	for _, provider := range []string{"aws", "google"} {
		for _, tunnel := range []bool{false, true} {
			for _, managedNAT := range []bool{false, true} {
				m := testConfigForm(t, provider, "v1.2.3-1")
				set := func(label, value string) { m.fields[indexForField(t, m, label)].value = value }
				set("Cluster domain (optional)", "demo.example.com")
				if provider == "google" {
					set("Google Cloud project ID", "test-project")
					set("DNS provider", "google-cloud-dns")
					set("Google Cloud DNS managed-zone resource name", "demo-zone")
				} else {
					set("DNS provider", "aws-route53")
				}
				set("ACME account email (optional)", "admin@example.com")
				if choices := m.fields[indexForField(t, m, "IP mode")].choices; len(choices) != 1 || choices[0] != "Dual-stack" {
					t.Fatalf("IP mode choices = %v", choices)
				}
				if choices := m.fields[indexForField(t, m, "NAT")].choices; choices[0] != "Cloud-provider NAT" {
					t.Fatalf("NAT choices = %v", choices)
				}
				if !managedNAT {
					set("NAT", "Nstance NAT")
				}
				if tunnel {
					m.index = indexForField(t, m, "What do you want to use for Kubernetes API access and cluster ingress?")
					m.input.SetValue("Cloudflare Tunnels")
					m.storeCurrentValue()
					m.domain = "demo.example.com"
					for _, label := range []string{"DNS provider", "ACME account email (optional)", "Google Cloud DNS managed-zone resource name"} {
						if m.fieldVisible(m.fields[indexForField(t, m, label)]) {
							t.Fatalf("tunnel shows %s", label)
						}
					}
				}
				cfg, err := m.config()
				if err != nil {
					t.Fatal(err)
				}
				p := cfg.Cluster.Providers[0]
				if p.VPC.V4CIDR == "" || p.VPC.V6CIDR != "auto" {
					t.Fatalf("incorrect VPC: %+v", p.VPC)
				}
				if strings.Join(cfg.Cluster.Kubernetes.ClusterCIDR, ",") != "100.64.0.0/10,fd64::/48" || strings.Join(cfg.Cluster.Kubernetes.ServiceCIDR, ",") != "198.18.0.0/15,fdc6::/108" {
					t.Fatal("Kubernetes IP families do not match infrastructure")
				}
				wantNAT := "nstance"
				if managedNAT {
					wantNAT = "provider"
				}
				if cfg.Cluster.NAT.Mode != wantNAT {
					t.Fatalf("NAT = %s, want %s", cfg.Cluster.NAT.Mode, wantNAT)
				}
				if tunnel {
					if cfg.Cluster.Tunnel == nil || cfg.Cluster.Tunnel.CredentialSecret != "tunnel-credentials" || len(p.LoadBalancers) != 0 || cfg.Cluster.ACME != nil || cfg.Cluster.Domains[0].Provider != nil || cfg.Cluster.Kubernetes.APIPort != 443 {
						t.Fatal("tunnel retained load balancer or DNS/ACME settings")
					}
					cfg.Cluster.Tunnel.ID = testTunnelID
				} else if len(p.LoadBalancers) != 1 || cfg.Cluster.Tunnel != nil {
					t.Fatal("NLB selection missing its load balancer")
				}
				cfg.Cluster.OIDC.IssuerURL = "https://auth.example.com"
				cfg.Cluster.Seed.Digest = "sha512:" + strings.Repeat("a", 128)
				if err := clusterconfig.Validate(cfg); err != nil {
					t.Errorf("%s tunnel=%v providerNAT=%v: %v", provider, tunnel, managedNAT, err)
				}
			}
		}
	}
}
