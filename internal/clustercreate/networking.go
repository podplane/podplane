// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustercreate

import "github.com/podplane/podplane/internal/clusterconfig"

// configureNetworking applies wizard choices to both infrastructure and Kubernetes.
func configureNetworking(cfg *clusterconfig.ClusterConfig, nat string) {
	provider := &cfg.Cluster.Providers[0]
	provider.VPC.V6CIDR = "auto"
	cfg.Cluster.Kubernetes.ClusterCIDR = []string{"100.64.0.0/10", "fd64::/48"}
	cfg.Cluster.Kubernetes.ServiceCIDR = []string{"198.18.0.0/15", "fdc6::/108"}
	cfg.Cluster.NAT = &clusterconfig.NAT{Mode: "provider"}
	if nat != "Cloud-provider NAT" {
		arch, instance := "arm64", "t4g.small"
		ladder := []string{"t4g.small", "t4g.medium", "t4g.large"}
		if provider.Kind == "google" {
			arch, instance = "amd64", "e2-small"
			ladder = []string{"e2-small", "e2-medium", "e2-standard-2"}
		}
		cfg.Cluster.NAT = &clusterconfig.NAT{
			Mode:    "nstance",
			Groups:  map[string]clusterconfig.NATGroup{"nat": {Arch: arch, InstanceType: instance, SubnetPool: "public"}},
			Tenants: map[string]clusterconfig.TenantNAT{"default": {Group: "nat", InstanceTypeLadder: ladder}},
		}
	}
	for zone, subnets := range provider.Zones {
		for i := range subnets {
			subnets[i].V6CIDR = "auto"
			if cfg.Cluster.NAT.Mode == "nstance" {
				for _, service := range subnets[i].Services {
					if service == "nstance" {
						subnets[i].Public = true
					}
				}
			}
		}
		provider.Zones[zone] = subnets
	}
}
