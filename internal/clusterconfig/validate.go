// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

var clusterIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var secretsProviderNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var domainLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateDomainName validates a fully qualified DNS name without a trailing
// dot or wildcard label.
func ValidateDomainName(name string) error {
	if name == "" {
		return fmt.Errorf("is required")
	}
	if len(name) > 253 {
		return fmt.Errorf("must be at most 253 characters")
	}
	if name != strings.ToLower(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("must be lowercase and must not end with a dot")
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return fmt.Errorf("must be a DNS hostname, not an IP address")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) > 63 || !domainLabelPattern.MatchString(label) {
			return fmt.Errorf("must contain only DNS labels")
		}
	}
	return nil
}

// ValidateClusterID validates a cluster ID using Netsy's identifier rules.
func ValidateClusterID(id string) error {
	if id == "" {
		return fmt.Errorf("is required")
	}
	if len(id) > 32 {
		return fmt.Errorf("must be at most 32 characters")
	}
	if strings.Contains(id, "--") {
		return fmt.Errorf("must not contain consecutive hyphens")
	}
	if !clusterIDPattern.MatchString(id) {
		return fmt.Errorf("must be lowercase alphanumeric with hyphens, no leading/trailing hyphens")
	}
	if id == "local" || id == "k8s" || id == "oidc" {
		return fmt.Errorf("%q is reserved", id)
	}
	return nil
}

// ValidateSeed validates the optional seed configuration.
func ValidateSeed(seed Seed) error {
	switch seed.Name {
	case "", "recommended", "minimal", "none":
	default:
		return fmt.Errorf("seed.name must be recommended, minimal, or none")
	}
	if seed.Name == "recommended" || seed.Name == "minimal" {
		if seed.Digest == "" {
			return fmt.Errorf("seed.digest is required")
		}
		algorithm, encoded, ok := strings.Cut(seed.Digest, ":")
		if !ok || algorithm != "sha512" || len(encoded) != 128 {
			return fmt.Errorf("seed.digest must be a sha512 digest in sha512:<hex> format")
		}
		for _, char := range encoded {
			if !strings.ContainsRune("0123456789abcdef", char) {
				return fmt.Errorf("seed.digest must be a sha512 digest in sha512:<hex> format")
			}
		}
	}
	return nil
}

// ValidateComponents validates the optional components configuration.
func ValidateComponents(components Components) error {
	if components.Registry != nil && components.Registry.Mirror.Prefix != "" {
		components.Registry.Mirror.Prefix = CleanRegistryMirrorPrefix(components.Registry.Mirror.Prefix)
	}
	if components.Source == nil {
		return nil
	}
	if strings.TrimSpace(components.Source.URL) == "" {
		return fmt.Errorf("components.source.url is required")
	}
	refCount := 0
	if components.Source.Ref.Branch != "" {
		refCount++
	}
	if components.Source.Ref.Tag != "" {
		refCount++
	}
	if components.Source.Ref.Semver != "" {
		refCount++
	}
	if components.Source.Ref.Commit != "" {
		refCount++
	}
	if refCount > 1 {
		return fmt.Errorf("components.source.ref must set at most one of branch, tag, semver, or commit")
	}
	if components.Source.SecretRef != nil && strings.TrimSpace(components.Source.SecretRef.Name) == "" {
		return fmt.Errorf("components.source.secretRef.name is required when secretRef is set")
	}
	return nil
}

// ValidateSecrets validates safe Podplane Secrets provider metadata.
func ValidateSecrets(secrets Secrets) error {
	if len(secrets.Providers) == 0 {
		if secrets.DefaultProvider != "" {
			return fmt.Errorf("default_provider requires at least one provider")
		}
		return nil
	}
	if secrets.DefaultProvider == "" {
		return fmt.Errorf("default_provider is required when providers are configured")
	}
	if err := validateSecretsProviderName("default_provider", secrets.DefaultProvider); err != nil {
		return err
	}
	for name, provider := range secrets.Providers {
		prefix := fmt.Sprintf("providers.%s", name)
		if err := validateSecretsProviderName(prefix, name); err != nil {
			return err
		}
		if provider.KeyPrefix != "" {
			if err := validateSecretsProviderName(prefix+".key_prefix", provider.KeyPrefix); err != nil {
				return err
			}
		}
		switch provider.Kind {
		case "aws":
			if provider.ObjectType != "secretsmanager" && provider.ObjectType != "ssmparameter" {
				return fmt.Errorf("%s.object_type must be secretsmanager or ssmparameter for aws", prefix)
			}
		case "gcp":
			if provider.ProjectID == "" {
				return fmt.Errorf("%s.project_id is required for gcp", prefix)
			}
		case "vault", "openbao":
			// Address, mount_path, ca_cert, auth_path, and operator_role are
			// operator/runtime routing fields and are intentionally stripped from
			// cached cluster summaries.
		case "":
			return fmt.Errorf("%s.kind is required", prefix)
		default:
			return fmt.Errorf("%s.kind must be aws, gcp, vault, or openbao", prefix)
		}
	}
	if _, ok := secrets.Providers[secrets.DefaultProvider]; !ok {
		return fmt.Errorf("default_provider %q does not match a configured provider", secrets.DefaultProvider)
	}
	return nil
}

func validateSecretsProviderName(prefix, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", prefix)
	}
	if len(name) > 32 || !secretsProviderNamePattern.MatchString(name) || strings.Contains(name, "--") {
		return fmt.Errorf("%s must be lowercase alphanumeric with hyphens, no leading/trailing hyphens, no consecutive hyphens, no dots, and at most 32 characters", prefix)
	}
	return nil
}

// Validate validates the cluster config fields that are required for managed
// OpenTofu/Terraform generation.
func Validate(cfg *ClusterConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	if err := ValidateClusterID(cfg.Cluster.ID); err != nil {
		return fmt.Errorf("cluster.id: %w", err)
	}
	if cfg.Cluster.OIDC.IssuerURL == "" {
		return fmt.Errorf("cluster.oidc.issuer_url is required")
	}
	if err := ValidateDomainName(cfg.Cluster.Kubernetes.APIHostname); err != nil {
		return fmt.Errorf("cluster.kubernetes.api_hostname: %w", err)
	}
	if port := cfg.Cluster.Kubernetes.APIPort; port < 0 || port > 65535 {
		return fmt.Errorf("cluster.kubernetes.api_port must be 1-65535 when set")
	}
	if hostname := cfg.Cluster.Registry.Hostname; hostname != "" {
		if err := ValidateDomainName(hostname); err != nil {
			return fmt.Errorf("cluster.registry.hostname: %w", err)
		}
	}
	if len(cfg.Cluster.Domains) > 0 && cfg.Cluster.Registry.Hostname == "" {
		return fmt.Errorf("cluster.registry.hostname is required when domains are configured")
	}
	if _, err := ServiceNetworkFromCIDRs(cfg.Cluster.Kubernetes.ServiceCIDR); err != nil {
		return fmt.Errorf("cluster.kubernetes.service_cidr: %w", err)
	}
	if cfg.Cluster.Components.Registry != nil && cfg.Cluster.Components.Registry.Mirror.Enabled && cfg.Cluster.RegistryMirrorHostname() == "" {
		return fmt.Errorf("cluster.registry.hostname is required when components.registry.mirror.enabled is true without components.registry.mirror.hostname")
	}
	if err := ValidateSeed(cfg.Cluster.Seed); err != nil {
		return fmt.Errorf("cluster.seed: %w", err)
	}
	if err := ValidateComponents(cfg.Cluster.Components); err != nil {
		return fmt.Errorf("cluster.components: %w", err)
	}
	if err := ValidateSecrets(cfg.Cluster.Secrets); err != nil {
		return fmt.Errorf("cluster.secrets: %w", err)
	}
	if len(cfg.Cluster.Providers) == 0 {
		return fmt.Errorf("cluster.providers must contain at least one provider")
	}
	if len(cfg.Cluster.Pools) == 0 {
		return fmt.Errorf("cluster.pools must contain at least one pool")
	}
	for name, pool := range cfg.Cluster.Pools {
		if name == "" {
			return fmt.Errorf("cluster.pools contains an empty pool name")
		}
		if pool.Arch != "amd64" && pool.Arch != "arm64" {
			return fmt.Errorf("cluster.pools.%s.arch must be amd64 or arm64", name)
		}
		if pool.InstanceType == "" {
			return fmt.Errorf("cluster.pools.%s.instance_type is required", name)
		}
		if pool.Size < 0 {
			return fmt.Errorf("cluster.pools.%s.size must be >= 0", name)
		}
	}
	for i, provider := range cfg.Cluster.Providers {
		if err := validateProvider(cfg, i, provider); err != nil {
			return err
		}
	}
	return validateDomains(cfg, cfg.Cluster.Providers[0])
}

// validateDomains validates domain names and their required load-balancer
// listeners.
func validateDomains(cfg *ClusterConfig, provider Provider) error {
	zones := make(map[string]bool, len(cfg.Cluster.Domains))
	for i, domain := range cfg.Cluster.Domains {
		prefix := fmt.Sprintf("cluster.domains[%d]", i)
		if err := ValidateDomainName(domain.Zone); err != nil {
			return fmt.Errorf("%s.zone: %w", prefix, err)
		}
		if zones[domain.Zone] {
			return fmt.Errorf("%s.zone duplicates %q", prefix, domain.Zone)
		}
		zones[domain.Zone] = true
		if domain.Provider != nil {
			switch domain.Provider.Kind {
			case "aws", "cloudflare", "google", "local":
			default:
				return fmt.Errorf("%s.provider.kind must be aws, cloudflare, google, or local", prefix)
			}
		}
		if err := requireListener(provider, domain.ResolvedDomainLoadBalancer(), 443, 443, ""); err != nil {
			return fmt.Errorf("%s.load_balancer: %w", prefix, err)
		}
	}
	if loadBalancer := cfg.Cluster.Kubernetes.APILoadBalancer; loadBalancer != "" {
		for _, domain := range cfg.Cluster.Domains {
			if domain.Zone == cfg.Cluster.Kubernetes.APIHostname && domain.ResolvedDomainLoadBalancer() != loadBalancer {
				return fmt.Errorf("cluster.kubernetes.api_load_balancer %q conflicts with domain apex %q load balancer %q", loadBalancer, domain.Zone, domain.ResolvedDomainLoadBalancer())
			}
		}
		port := cfg.Cluster.Kubernetes.APIPort
		if port == 0 {
			port = 6443
		}
		if err := requireListener(provider, loadBalancer, port, 6443, "control-plane"); err != nil {
			return fmt.Errorf("cluster.kubernetes.api_load_balancer: %w", err)
		}
	}
	return nil
}

// requireListener verifies that a named load balancer has the required port,
// target, and pool.
func requireListener(provider Provider, loadBalancer string, port, targetPort int, pool string) error {
	lb, ok := provider.LoadBalancers[loadBalancer]
	if !ok {
		return fmt.Errorf("references unknown load balancer %q", loadBalancer)
	}
	for _, listener := range lb.Listeners {
		targetPortValue := listener.TargetPort
		if targetPortValue == 0 {
			targetPortValue = listener.Port
		}
		if listener.Port == port && targetPortValue == targetPort && (pool == "" || listener.Pool == pool) {
			return nil
		}
	}
	if pool != "" {
		return fmt.Errorf("load balancer %q requires listener port %d targeting port %d for pool %q", loadBalancer, port, targetPort, pool)
	}
	return fmt.Errorf("load balancer %q requires listener port %d targeting port %d", loadBalancer, port, targetPort)
}

func validateProvider(cfg *ClusterConfig, index int, provider Provider) error {
	prefix := fmt.Sprintf("cluster.providers[%d]", index)
	switch provider.Kind {
	case "aws":
		if provider.Region == "" {
			return fmt.Errorf("%s.region is required for aws", prefix)
		}
	case "google":
		return fmt.Errorf("%s.kind google is not supported by cluster create yet", prefix)
	case "proxmox":
		return fmt.Errorf("%s.kind proxmox is not supported by cluster create", prefix)
	default:
		return fmt.Errorf("%s.kind must be aws", prefix)
	}
	if provider.VPC.ID != "" && (provider.VPC.V4CIDR != "" || provider.VPC.V6CIDR != "") {
		return fmt.Errorf("%s.vpc.id cannot be combined with vpc CIDRs", prefix)
	}
	if provider.VPC.ID == "" && provider.VPC.V4CIDR == "" {
		return fmt.Errorf("%s.vpc.id or vpc.v4cidr is required", prefix)
	}
	if provider.VPC.V6CIDR != "" && provider.VPC.V6CIDR != "auto" {
		return fmt.Errorf("%s.vpc.v6cidr only supports \"auto\" for managed AWS generation", prefix)
	}
	if len(provider.Zones) == 0 {
		return fmt.Errorf("%s.zones must contain at least one zone", prefix)
	}
	subnetRoles := make(map[string]bool)
	for zone, subnets := range provider.Zones {
		if zone == "" {
			return fmt.Errorf("%s.zones contains an empty zone name", prefix)
		}
		if len(subnets) == 0 {
			return fmt.Errorf("%s.zones.%s must contain at least one subnet", prefix, zone)
		}
		for i, subnet := range subnets {
			if err := validateSubnet(cfg, fmt.Sprintf("%s.zones.%s[%d]", prefix, zone, i), subnet); err != nil {
				return err
			}
			subnetRoles[subnet.ResolvedRole()] = true
		}
	}
	buckets := map[string]bool{}
	for _, bucket := range provider.Buckets {
		buckets[bucket] = true
	}
	for name, role := range provider.Roles {
		for _, bucket := range role.Buckets {
			if !buckets[bucket] {
				return fmt.Errorf("%s.roles.%s.buckets references unknown bucket %q", prefix, name, bucket)
			}
		}
		if role.Permissions != "" && role.Permissions != "read-write" && role.Permissions != "read-only" {
			return fmt.Errorf("%s.roles.%s.permissions must be read-write or read-only", prefix, name)
		}
	}
	for name, loadBalancer := range provider.LoadBalancers {
		if name == "" {
			return fmt.Errorf("%s.load_balancers contains an empty name", prefix)
		}
		if loadBalancer.Subnets == "" {
			return fmt.Errorf("%s.load_balancers.%s.subnets is required", prefix, name)
		}
		if !subnetRoles[loadBalancer.Subnets] {
			return fmt.Errorf("%s.load_balancers.%s.subnets references unknown subnet role %q", prefix, name, loadBalancer.Subnets)
		}
		ports := make(map[int]bool, len(loadBalancer.Listeners))
		for i, listener := range loadBalancer.Listeners {
			listenerPrefix := fmt.Sprintf("%s.load_balancers.%s.listeners[%d]", prefix, name, i)
			if listener.Port < 1 || listener.Port > 65535 {
				return fmt.Errorf("%s.port must be 1-65535", listenerPrefix)
			}
			if ports[listener.Port] {
				return fmt.Errorf("%s.port duplicates port %d", listenerPrefix, listener.Port)
			}
			ports[listener.Port] = true
			if listener.TargetPort < 0 || listener.TargetPort > 65535 {
				return fmt.Errorf("%s.target_port must be 1-65535 when set", listenerPrefix)
			}
			if _, ok := cfg.Cluster.Pools[listener.Pool]; !ok {
				return fmt.Errorf("%s.pool references unknown pool %q", listenerPrefix, listener.Pool)
			}
		}
	}
	return nil
}

func validateSubnet(cfg *ClusterConfig, prefix string, subnet Subnet) error {
	if subnet.ID != "" && (subnet.V4CIDR != "" || subnet.V6CIDR != "") {
		return fmt.Errorf("%s.id cannot be combined with subnet CIDRs", prefix)
	}
	if subnet.ID == "" && subnet.V4CIDR == "" {
		return fmt.Errorf("%s.id or v4cidr is required", prefix)
	}
	if subnet.V6CIDR != "" && subnet.V6CIDR != "auto" {
		return fmt.Errorf("%s.v6cidr only supports \"auto\" for managed AWS generation", prefix)
	}
	hasPool := subnet.Pool != ""
	hasServices := len(subnet.Services) > 0
	if hasPool == hasServices {
		return fmt.Errorf("%s must set exactly one of pool or services", prefix)
	}
	if hasPool {
		if _, ok := cfg.Cluster.Pools[subnet.Pool]; !ok {
			return fmt.Errorf("%s.pool references unknown pool %q", prefix, subnet.Pool)
		}
		return nil
	}
	for _, service := range subnet.Services {
		switch service {
		case "nstance":
		case "nat", "nlb":
			if !subnet.Public {
				return fmt.Errorf("%s with service %q must be public", prefix, service)
			}
		default:
			return fmt.Errorf("%s.services contains unsupported service %q", prefix, service)
		}
	}
	return nil
}
