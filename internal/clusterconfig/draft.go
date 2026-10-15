// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import "fmt"

// NewDraftConfig returns a mutable draft cluster config for the requested
// provider, rejecting empty or unsupported provider kinds.
func NewDraftConfig(providerKind string) (*ClusterConfig, error) {
	cfg := copyClusterConfig(defaultDraftConfig)
	provider := Provider{Kind: providerKind, VPC: VPC{V4CIDR: "172.18.0.0/16"}}
	var zone string
	var pool Pool
	switch providerKind {
	case "google":
		provider.Region = "us-central1"
		zone = "us-central1-a"
		pool = Pool{Arch: "amd64", InstanceType: "e2-medium", Size: 3}
		cfg.Cluster.Secrets = Secrets{
			DefaultProvider: "gcp-secret-manager",
			Providers: map[string]SecretsProvider{
				"gcp-secret-manager": {Kind: "gcp"},
			},
		}
	case "aws":
		provider.Region = "us-east-1"
		provider.VPC.V6CIDR = "auto"
		zone = "us-east-1a"
		pool = Pool{Arch: "arm64", InstanceType: "t4g.medium", Size: 3}
		cfg.Cluster.Secrets = Secrets{
			DefaultProvider: "aws-secrets-manager",
			Providers: map[string]SecretsProvider{
				"aws-secrets-manager": {
					Kind:       "aws",
					ObjectType: "secretsmanager",
				},
			},
		}
	default:
		return nil, fmt.Errorf("unsupported cloud provider %q; must be aws or google", providerKind)
	}
	provider.Zones = map[string][]Subnet{
		zone: {
			{V4CIDR: "172.18.10.0/28", V6CIDR: provider.VPC.V6CIDR, Services: []string{"nat", "nlb"}, Public: true},
			{V4CIDR: "172.18.20.0/28", V6CIDR: provider.VPC.V6CIDR, Services: []string{"nstance"}},
			{V4CIDR: "172.18.1.0/26", V6CIDR: provider.VPC.V6CIDR, Pool: "control-plane"},
		},
	}
	cfg.Cluster.Providers = []Provider{provider}
	cfg.Cluster.Pools = map[string]Pool{"control-plane": pool}
	cfg.Cluster.SPIFFE.TrustDomain = cfg.Cluster.Kubernetes.APIHostname
	return &cfg, nil
}

// defaultDraftConfig holds provider-independent cluster defaults.
var defaultDraftConfig = ClusterConfig{Cluster: Cluster{
	ID:   "example-cluster",
	Name: "Example Cluster",
	OIDC: OIDC{IssuerURL: "https://auth.example.com"},
	Registry: Registry{
		Hostname: "example-cluster-registry.local",
	},
	Kubernetes: Kubernetes{
		APIHostname: "example-cluster.k8s.local",
		ClusterCIDR: []string{"100.64.0.0/10"},
		ServiceCIDR: []string{"198.18.0.0/15"},
	},
}}

// copyClusterConfig returns a deep enough copy for wizard mutation.
func copyClusterConfig(in ClusterConfig) ClusterConfig {
	out := in
	out.Cluster.Domains = append([]Domain(nil), in.Cluster.Domains...)
	out.Cluster.Providers = append([]Provider(nil), in.Cluster.Providers...)
	for i := range out.Cluster.Providers {
		provider := &out.Cluster.Providers[i]
		provider.LoadBalancers = make(map[string]LoadBalancer, len(in.Cluster.Providers[i].LoadBalancers))
		for name, loadBalancer := range in.Cluster.Providers[i].LoadBalancers {
			loadBalancer.Listeners = append([]Listener(nil), loadBalancer.Listeners...)
			provider.LoadBalancers[name] = loadBalancer
		}
	}
	out.Cluster.Kubernetes.ClusterCIDR = append([]string(nil), in.Cluster.Kubernetes.ClusterCIDR...)
	out.Cluster.Kubernetes.ServiceCIDR = append([]string(nil), in.Cluster.Kubernetes.ServiceCIDR...)
	if in.Cluster.Components.Source != nil {
		source := *in.Cluster.Components.Source
		out.Cluster.Components.Source = &source
	}
	if in.Cluster.Components.Registry != nil {
		registry := *in.Cluster.Components.Registry
		out.Cluster.Components.Registry = &registry
	}
	out.Cluster.Pools = make(map[string]Pool, len(in.Cluster.Pools))
	for name, pool := range in.Cluster.Pools {
		out.Cluster.Pools[name] = pool
	}
	return out
}
