// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

// NewDraftConfig returns a mutable draft cluster config for the requested
// provider.
func NewDraftConfig(providerKind string) *ClusterConfig {
	cfg := copyClusterConfig(defaultDraftConfig)
	if providerKind == "" {
		providerKind = "aws"
	}
	cfg.Cluster.Providers = []Provider{newDraftProvider(providerKind)}
	cfg.Cluster.Secrets = newDraftSecrets(providerKind)
	return &cfg
}

// newDraftProvider returns provider-specific draft infrastructure settings.
func newDraftProvider(kind string) Provider {
	switch kind {
	case "aws":
		return Provider{
			Kind:   "aws",
			Region: "us-east-1",
			VPC: VPC{
				V4CIDR: "172.18.0.0/16",
				V6CIDR: "auto",
			},
			Zones: map[string][]Subnet{
				"us-east-1a": {
					{V4CIDR: "172.18.10.0/28", V6CIDR: "auto", Services: []string{"nat", "nlb"}, Public: true},
					{V4CIDR: "172.18.20.0/28", V6CIDR: "auto", Services: []string{"nstance"}},
					{V4CIDR: "172.18.1.0/24", V6CIDR: "auto", Pool: "control-plane"},
				},
			},
		}
	default:
		return Provider{Kind: kind}
	}
}

func newDraftSecrets(providerKind string) Secrets {
	switch providerKind {
	case "aws":
		return Secrets{
			DefaultProvider: "aws-secrets-manager",
			Providers: map[string]SecretsProvider{
				"aws-secrets-manager": {
					Kind:       "aws",
					ObjectType: "secretsmanager",
				},
			},
		}
	default:
		return Secrets{}
	}
}

var defaultDraftConfig = ClusterConfig{Cluster: Cluster{
	ID:   "example-cluster",
	Name: "Example Cluster",
	OIDC: OIDC{IssuerURL: "https://auth.example.com"},
	Registry: Registry{
		Hostname: "example-cluster-registry.local",
	},
	Pools: map[string]Pool{
		"control-plane": {
			Arch:         "arm64",
			InstanceType: "t4g.medium",
			Size:         3,
		},
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
