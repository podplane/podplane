// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestComponentsUnmarshalObject verifies object-form component sources decode.
func TestComponentsUnmarshalObject(t *testing.T) {
	var cfg ClusterConfig
	if err := json.Unmarshal([]byte(`{"cluster":{"components":{"source":{"url":"https://github.com/example/components.git","ref":{"branch":"feature"}}}}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got, want := cfg.Cluster.Components.Source.URL, "https://github.com/example/components.git"; got != want {
		t.Fatalf("components.source.url = %q, want %q", got, want)
	}
	if got, want := cfg.Cluster.Components.Source.Ref.Branch, "feature"; got != want {
		t.Fatalf("components.source.ref.branch = %q, want %q", got, want)
	}
}

// TestValidateComponentsRejectsMultipleSourceRefs rejects ambiguous source revisions.
func TestValidateComponentsRejectsMultipleSourceRefs(t *testing.T) {
	err := ValidateComponents(Components{Source: &ComponentsSource{Ref: ComponentsSourceRef{Branch: "main", Tag: "v1.0.0"}}})
	if err == nil {
		t.Fatal("ValidateComponents returned nil, want error")
	}
}

// TestValidateComponentsRejectsEmptySourceSecretRefName rejects an unnamed source credential.
func TestValidateComponentsRejectsEmptySourceSecretRefName(t *testing.T) {
	err := ValidateComponents(Components{Source: &ComponentsSource{URL: "https://github.com/example/components.git", SecretRef: &ComponentsSourceSecretRef{}}})
	if err == nil {
		t.Fatal("ValidateComponents returned nil, want error")
	}
}

// TestValidateACME verifies ACME account fields and supported DNS-provider requirements.
func TestValidateACME(t *testing.T) {
	awsDomain := []Domain{{Zone: "example.com", Provider: &DomainProvider{Kind: "aws-route53"}}}
	if err := ValidateACME(&ACME{Email: "ops@example.com"}, awsDomain); err != nil {
		t.Fatalf("ValidateACME returned error: %v", err)
	}
	if err := ValidateACME(&ACME{Email: "invalid"}, awsDomain); err == nil {
		t.Fatal("ValidateACME accepted invalid email")
	}
	if err := ValidateACME(&ACME{Email: "ops@example.com"}, []Domain{{Zone: "example.com"}}); err == nil {
		t.Fatal("ValidateACME accepted config without a supported ACME DNS provider")
	}
}

// TestValidateSeedDigest accepts only required SHA-512 seed digests.
func TestValidateSeedDigest(t *testing.T) {
	for _, name := range []string{"minimal", "platform", "agentic"} {
		if err := ValidateSeed(Seed{Name: name, Digest: "sha512:" + strings.Repeat("a", 128)}); err != nil {
			t.Fatalf("ValidateSeed returned error for valid digest: %v", err)
		}
		if err := ValidateSeed(Seed{Name: name}); err == nil {
			t.Fatal("ValidateSeed returned nil without required digest")
		}
		if err := ValidateSeed(Seed{Name: name, Digest: "sha512:invalid"}); err == nil {
			t.Fatal("ValidateSeed returned nil for invalid digest")
		}
	}
	for _, name := range []string{"", "none"} {
		if err := ValidateSeed(Seed{Name: name}); err != nil {
			t.Fatalf("ValidateSeed(%q): %v", name, err)
		}
	}
	for _, name := range []string{"recommended", "app"} {
		if err := ValidateSeed(Seed{Name: name, Digest: "sha512:" + strings.Repeat("a", 128)}); err == nil {
			t.Fatalf("ValidateSeed accepted removed %s profile", name)
		}
	}
}

// TestValidateClusterID covers valid, malformed, and reserved cluster IDs.
func TestValidateClusterID(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		expectErr bool
	}{
		{name: "valid", id: "test-cluster-123"},
		{name: "default", id: "default"},
		{name: "one letter", id: "a"},
		{name: "one number", id: "1"},
		{name: "starts with number", id: "1test"},
		{name: "empty", id: "", expectErr: true},
		{name: "invalid characters", id: "test@cluster", expectErr: true},
		{name: "too long", id: "this-is-a-very-long-cluster-id-that-exceeds-the-maximum", expectErr: true},
		{name: "spaces", id: "test cluster", expectErr: true},
		{name: "uppercase", id: "Test-Cluster", expectErr: true},
		{name: "starts with hyphen", id: "-test", expectErr: true},
		{name: "ends with hyphen", id: "test-", expectErr: true},
		{name: "consecutive hyphens", id: "test--cluster", expectErr: true},
		{name: "underscore", id: "test_cluster", expectErr: true},
		{name: "reserved local", id: "local", expectErr: true},
		{name: "reserved k8s", id: "k8s", expectErr: true},
		{name: "reserved oidc", id: "oidc", expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClusterID(tt.id)
			if (err != nil) != tt.expectErr {
				t.Fatalf("ValidateClusterID(%q) error = %v, expectErr %v", tt.id, err, tt.expectErr)
			}
		})
	}
}

// TestValidateTrustDomain covers the supported SPIFFE trust-domain syntax.
func TestValidateTrustDomain(t *testing.T) {
	for _, domain := range []string{"k8s.example.com", "mesh_test-1"} {
		if err := ValidateTrustDomain(domain); err != nil {
			t.Errorf("ValidateTrustDomain(%q) returned error: %v", domain, err)
		}
	}
	for _, domain := range []string{"", "https://example.com", "Example.com", "example.com:443", strings.Repeat("a", 256)} {
		if err := ValidateTrustDomain(domain); err == nil {
			t.Errorf("ValidateTrustDomain(%q) returned nil, want error", domain)
		}
	}
}

// TestValidateWorkloadCAProvider accepts providers that can mount a workload CA key.
func TestValidateWorkloadCAProvider(t *testing.T) {
	for _, provider := range []SecretsProvider{
		{Kind: "aws", ObjectType: "secretsmanager"},
		{Kind: "aws", ObjectType: "ssmparameter"},
		{Kind: "gcp", ProjectID: "example-project"},
		{Kind: "vault", Address: "https://vault.example"},
		{Kind: "openbao", Address: "https://openbao.example"},
	} {
		secrets := Secrets{DefaultProvider: "default", Providers: map[string]SecretsProvider{"default": provider}}
		if err := ValidateWorkloadCAProvider(secrets); err != nil {
			t.Errorf("ValidateWorkloadCAProvider(%+v) returned error: %v", provider, err)
		}
	}
	for _, kind := range []string{"vault", "openbao"} {
		for _, address := range []string{"", "http://vault.example"} {
			secrets := Secrets{DefaultProvider: "default", Providers: map[string]SecretsProvider{"default": {Kind: kind, Address: address}}}
			if err := ValidateWorkloadCAProvider(secrets); err == nil {
				t.Errorf("ValidateWorkloadCAProvider accepted %s address %q", kind, address)
			}
		}
	}
}

// TestValidateSecretsRequiresVaultHTTPS verifies every Vault-compatible
// provider uses a credential-free HTTPS endpoint, even when it is not default.
func TestValidateSecretsRequiresVaultHTTPS(t *testing.T) {
	for _, address := range []string{"", "http://vault.example", "https://user@vault.example", "https://vault.example?token=value"} {
		secrets := Secrets{DefaultProvider: "aws", Providers: map[string]SecretsProvider{
			"aws":   {Kind: "aws", ObjectType: "secretsmanager"},
			"vault": {Kind: "vault", Address: address},
		}}
		if err := ValidateSecrets(secrets); err == nil {
			t.Errorf("ValidateSecrets accepted Vault address %q", address)
		}
	}
	secrets := Secrets{DefaultProvider: "aws", Providers: map[string]SecretsProvider{
		"aws":     {Kind: "aws", ObjectType: "secretsmanager"},
		"openbao": {Kind: "openbao", Address: "https://openbao.example"},
	}}
	if err := ValidateSecrets(secrets); err != nil {
		t.Fatalf("ValidateSecrets rejected HTTPS OpenBao address: %v", err)
	}
}

// TestValidateAWSRegionAndAvailabilityZone covers standard, partition-specific,
// local, malformed, and cross-region AWS location names.
func TestValidateAWSRegionAndAvailabilityZone(t *testing.T) {
	for _, region := range []string{"us-west-2", "us-gov-west-1", "cn-north-1"} {
		if err := ValidateAWSRegion(region); err != nil {
			t.Errorf("ValidateAWSRegion(%q) returned error: %v", region, err)
		}
	}
	for _, region := range []string{"", "us-west", "us_west_2", "west-2"} {
		if err := ValidateAWSRegion(region); err == nil {
			t.Errorf("ValidateAWSRegion(%q) returned nil, want error", region)
		}
	}
	for _, zone := range []string{"us-west-2a", "us-west-2-lax-1a"} {
		if err := ValidateAWSAvailabilityZone("us-west-2", zone); err != nil {
			t.Errorf("ValidateAWSAvailabilityZone(%q) returned error: %v", zone, err)
		}
	}
	for _, zone := range []string{"us-west-1a", "us-west-20a", "us-west-2"} {
		if err := ValidateAWSAvailabilityZone("us-west-2", zone); err == nil {
			t.Errorf("ValidateAWSAvailabilityZone(%q) returned nil, want error", zone)
		}
	}
}

// TestValidateDomainsAndListeners covers domain uniqueness, references, and
// required listener mappings.
func TestValidateDomainsAndListeners(t *testing.T) {
	cfg := validManagedConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	cfg.Cluster.Domains = append(cfg.Cluster.Domains, cfg.Cluster.Domains[0])
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("Validate duplicate domain error = %v, want duplicate error", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Kubernetes.APIPort = 443
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "requires listener port 443 targeting port 6443 for pool \"control-plane\"") {
		t.Fatalf("Validate missing API listener error = %v, want required listener error", err)
	}

	cfg = validManagedConfig()
	lb := cfg.Cluster.Providers[0].LoadBalancers["main"]
	lb.Listeners[1].Pool = "ingress"
	cfg.Cluster.Pools["ingress"] = Pool{Arch: "arm64", InstanceType: "t4g.medium", Size: 1}
	cfg.Cluster.Providers[0].LoadBalancers["main"] = lb
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "for pool \"control-plane\"") {
		t.Fatalf("Validate API listener pool error = %v, want control-plane requirement", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Domains[0].LoadBalancer = "missing"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "unknown load balancer") {
		t.Fatalf("Validate missing load balancer error = %v, want reference error", err)
	}

	cfg = validManagedConfig()
	lb = cfg.Cluster.Providers[0].LoadBalancers["main"]
	lb.Listeners = append(lb.Listeners, Listener{Port: 443, Pool: "control-plane"})
	cfg.Cluster.Providers[0].LoadBalancers["main"] = lb
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "duplicates port 443") {
		t.Fatalf("Validate duplicate listener error = %v, want duplicate port error", err)
	}

	cfg = validManagedConfig()
	lb = cfg.Cluster.Providers[0].LoadBalancers["main"]
	lb.Subnets = "missing"
	cfg.Cluster.Providers[0].LoadBalancers["main"] = lb
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "unknown subnet role") {
		t.Fatalf("Validate unknown load-balancer subnet role error = %v, want reference error", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Providers[0].LoadBalancers["kubernetes-api"] = LoadBalancer{
		Public: true, Subnets: "public",
		Listeners: []Listener{{Port: 443, TargetPort: 6443, ProxyPort: new(16443), Pool: "control-plane"}},
	}
	cfg.Cluster.Kubernetes.APILoadBalancer = "kubernetes-api"
	cfg.Cluster.Kubernetes.APIPort = 443
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate separate port-443 API load balancer returned error: %v", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Providers[0].LoadBalancers["kubernetes-api"] = LoadBalancer{
		Public: true, Subnets: "public",
		Listeners: []Listener{{Port: 6443, ProxyPort: new(16443), Pool: "control-plane"}},
	}
	cfg.Cluster.Kubernetes.APIHostname = "example.com"
	cfg.Cluster.Kubernetes.APILoadBalancer = "kubernetes-api"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "conflicts with domain apex") {
		t.Fatalf("Validate split apex error = %v, want DNS ownership conflict", err)
	}
}

// TestValidateDNSProviderKinds preserves provider metadata used by seeded
// DNS-01 configuration independently of Terraform DNS support.
func TestValidateDNSProviderKinds(t *testing.T) {
	for _, kind := range []string{"aws-route53", "cloudflare", "google-cloud-dns", "local"} {
		t.Run(kind, func(t *testing.T) {
			cfg := validManagedConfig()
			cfg.Cluster.Domains[0].Provider.Kind = kind
			if err := Validate(cfg); err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
		})
	}

	cfg := validManagedConfig()
	cfg.Cluster.Domains[0].Provider.Kind = "other"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "must be aws-route53, cloudflare, google-cloud-dns, or local") {
		t.Fatalf("Validate unknown DNS provider error = %v, want supported-kind error", err)
	}
}

// TestValidateAWSNetworkPlacement rejects values that AWS would otherwise
// reject only after Terraform has started creating unrelated resources.
func TestValidateAWSNetworkPlacement(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Provider)
		wantErr string
	}{
		{
			name: "zone outside region",
			mutate: func(provider *Provider) {
				provider.Zones["us-west-1a"] = provider.Zones["us-east-1a"]
				delete(provider.Zones, "us-east-1a")
			},
			wantErr: "must be an availability zone in region us-east-1",
		},
		{
			name: "subnet outside VPC",
			mutate: func(provider *Provider) {
				subnets := provider.Zones["us-east-1a"]
				subnets[0].V4CIDR = "172.19.1.0/24"
				provider.Zones["us-east-1a"] = subnets
			},
			wantErr: "must be contained within VPC CIDR 172.18.0.0/16",
		},
		{
			name: "invalid VPC CIDR",
			mutate: func(provider *Provider) {
				provider.VPC.V4CIDR = "172.18.0.1/16"
			},
			wantErr: "must be a canonical IPv4 CIDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validManagedConfig()
			tt.mutate(&cfg.Cluster.Providers[0])
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateRequiresKubernetesAPIHostname verifies the endpoint is explicit.
func TestValidateRequiresKubernetesAPIHostname(t *testing.T) {
	cfg := validManagedConfig()
	cfg.Cluster.Kubernetes.APIHostname = ""
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "api_hostname") {
		t.Fatalf("Validate missing API hostname error = %v, want required error", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Kubernetes.APIHostname = "127.0.0.1"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "not an IP address") {
		t.Fatalf("Validate IP API hostname error = %v, want hostname error", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Registry.Hostname = ""
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "registry.hostname is required") {
		t.Fatalf("Validate missing domain registry hostname error = %v, want required error", err)
	}
}

// TestResolvedDomainAndRegistryDefaults verifies stable config defaults.
func TestResolvedDomainAndRegistryDefaults(t *testing.T) {
	cfg := &ClusterConfig{Cluster: Cluster{ID: "example", Domains: []Domain{{Zone: "example.com"}}}}
	if got, want := cfg.DefaultDomain().Zone, "example.com"; got != want {
		t.Fatalf("DefaultDomain zone = %q, want %q", got, want)
	}
	if got, want := cfg.Cluster.Domains[0].ResolvedDomainLoadBalancer(), "main"; got != want {
		t.Fatalf("ResolvedDomainLoadBalancer = %q, want %q", got, want)
	}
	registryCfg := &ClusterConfig{Cluster: Cluster{ID: "example"}}
	if got, want := registryCfg.ResolvedRegistryHostname(), "example-registry.local"; got != want {
		t.Fatalf("ResolvedRegistryHostname = %q, want %q", got, want)
	}
}

// validManagedConfig returns a minimal valid AWS cluster with a shared ingress
// and Kubernetes API load balancer.
func validManagedConfig() *ClusterConfig {
	return &ClusterConfig{Cluster: Cluster{
		ID:       "example",
		OIDC:     OIDC{IssuerURL: "https://auth.example.com"},
		SPIFFE:   SPIFFE{TrustDomain: "k8s.example.com"},
		Domains:  []Domain{{Zone: "example.com", Provider: &DomainProvider{Kind: "aws-route53"}}},
		Registry: Registry{Hostname: "registry.example.com"},
		Kubernetes: Kubernetes{
			APIHostname:     "k8s.example.com",
			APILoadBalancer: "main",
		},
		Pools: map[string]Pool{
			"control-plane": {Arch: "arm64", InstanceType: "t4g.medium", Size: 1},
		},
		Providers: []Provider{{
			Kind:   "aws",
			Region: "us-east-1",
			VPC:    VPC{V4CIDR: "172.18.0.0/16"},
			Zones: map[string][]Subnet{
				"us-east-1a": {
					{V4CIDR: "172.18.1.0/24", Pool: "control-plane"},
					{V4CIDR: "172.18.10.0/28", Services: []string{"nat", "nlb"}, Public: true},
				},
			},
			LoadBalancers: map[string]LoadBalancer{
				"main": {
					Public:  true,
					Subnets: "public",
					Listeners: []Listener{
						{Port: 443, Pool: "control-plane"},
						{Port: 6443, Pool: "control-plane"},
					},
				},
			},
		}},
	}}
}

// networkContractConfig builds an AWS or Google fixture with cluster-wide exposure and NAT settings.
func networkContractConfig(t *testing.T, kind, exposure string) *ClusterConfig {
	t.Helper()
	cfg, err := NewDraftConfig("aws")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Cluster.Seed = Seed{Name: "platform", Version: "v2.0.0", Digest: "sha512:" + strings.Repeat("0", 128)}
	cfg.Cluster.Kubernetes.APILoadBalancer = "api"
	p := &cfg.Cluster.Providers[0]
	p.LoadBalancers = map[string]LoadBalancer{
		"api":     {Public: true, Subnets: "public", Listeners: []Listener{{Port: 6443, Pool: "control-plane"}}},
		"ingress": {Public: true, Subnets: "public", Listeners: []Listener{{Port: 443, Pool: "control-plane"}}},
	}
	if exposure == "tunnel" {
		p.LoadBalancers = nil
		cfg.Cluster.Kubernetes.APILoadBalancer = ""
		cfg.Cluster.Tunnel = &Tunnel{
			Kind: "cloudflare",
			ID:   "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8", CredentialSecret: "tunnel-credentials",
		}
	}
	cfg.Cluster.NAT = &NAT{
		Mode:    "provider",
		Groups:  map[string]NATGroup{"nat": {Arch: "arm64", InstanceType: "small", SubnetPool: "public"}},
		Tenants: map[string]TenantNAT{"default": {Group: "nat", InstanceTypeLadder: []string{"small", "medium", "large"}}},
	}
	if kind == "google" {
		p.Kind, p.Region, p.Project = "google", "us-central1", "example-project"
		p.Zones = map[string][]Subnet{"us-central1-a": p.Zones["us-east-1a"]}
	}
	return cfg
}

// TestNetworkingContractsRoundTrip checks routing and NAT combinations survive persistence with correct API trust.
func TestNetworkingContractsRoundTrip(t *testing.T) {
	for _, provider := range []string{"aws", "google"} {
		for _, exposure := range []string{"nlb", "tunnel"} {
			for _, nat := range []string{"provider", "nstance"} {
				t.Run(provider+"/"+exposure+"/"+nat, func(t *testing.T) {
					cfg := networkContractConfig(t, provider, exposure)
					cfg.Cluster.NAT.Mode = nat
					if nat == "nstance" {
						for zone, subnets := range cfg.Cluster.Providers[0].Zones {
							subnets[1].Public = true
							cfg.Cluster.Providers[0].Zones[zone] = subnets
						}
					}
					if err := Validate(cfg); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(t.TempDir(), "cluster.jsonc")
					if err := Write(path, cfg); err != nil {
						t.Fatal(err)
					}
					got, err := Load(path)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got.Cluster, cfg.Cluster) {
						t.Fatal("networking contracts changed during round trip")
					}
					want := "https://example-cluster.k8s.local:6443"
					if exposure == "tunnel" {
						want = "https://example-cluster.k8s.local:443"
					}
					if got.ResolvedKubernetesAPIURL() != want || got.UsesPublicAPIRoots() != (exposure == "tunnel") {
						t.Fatalf("wrong API endpoint/trust: %s, public=%v", got.ResolvedKubernetesAPIURL(), got.UsesPublicAPIRoots())
					}
				})
			}
		}
	}
}

// TestNetworkingContractsRejectUnsafeConfigurations checks routing collisions and invalid tunnel and NAT settings.
func TestNetworkingContractsRejectUnsafeConfigurations(t *testing.T) {
	for _, test := range []struct {
		name, exposure, want string
		change               func(*ClusterConfig)
	}{
		{"tunnel with ACME", "tunnel", "cluster.acme cannot be combined with cluster.tunnel", func(c *ClusterConfig) {
			c.Cluster.Domains = []Domain{{Zone: "example.com", Provider: &DomainProvider{Kind: "aws-route53"}}}
			c.Cluster.Registry.Hostname = "registry.example.com"
			c.Cluster.ACME = &ACME{Email: "ops@example.com"}
		}},
		{"tunnel with NLB", "tunnel", "cannot be combined", func(c *ClusterConfig) {
			c.Cluster.Providers[0].LoadBalancers = map[string]LoadBalancer{
				"ingress": {Public: true, Subnets: "public", Listeners: []Listener{{Port: 443, Pool: "control-plane"}}},
			}
		}},
		{"API on workers", "nlb", "for pool \"control-plane\"", func(c *ClusterConfig) {
			c.Cluster.Pools["worker"] = Pool{Arch: "arm64", InstanceType: "small", Size: 1}
			lb := c.Cluster.Providers[0].LoadBalancers["api"]
			lb.Listeners[0].Pool = "worker"
			c.Cluster.Providers[0].LoadBalancers["api"] = lb
		}},
		{"wrong edge port", "tunnel", "443", func(c *ClusterConfig) {
			c.Cluster.Kubernetes.APIPort = 6443
		}},
		{"proxy collision", "nlb", "collides", func(c *ClusterConfig) {
			lb := c.Cluster.Providers[0].LoadBalancers["ingress"]
			lb.Listeners[0].ProxyPort = new(6443)
			c.Cluster.Providers[0].LoadBalancers["ingress"] = lb
		}},
		{"default proxy collision with remapped target", "nlb", "collides", func(c *ClusterConfig) {
			c.Cluster.Kubernetes.APIPort = 34443
			lb := c.Cluster.Providers[0].LoadBalancers["api"]
			lb.Listeners[0].Port = 34443
			lb.Listeners[0].TargetPort = 6443
			c.Cluster.Providers[0].LoadBalancers["api"] = lb
			lb = c.Cluster.Providers[0].LoadBalancers["ingress"]
			lb.Listeners[0].ProxyPort = new(6443)
			c.Cluster.Providers[0].LoadBalancers["ingress"] = lb
		}},
		{"server collision", "nlb", "server port", func(c *ClusterConfig) {
			lb := c.Cluster.Providers[0].LoadBalancers["ingress"]
			lb.Listeners[0].ProxyPort = new(8992)
			c.Cluster.Providers[0].LoadBalancers["ingress"] = lb
		}},
		{"private nst", "nlb", "public/direct", func(c *ClusterConfig) { c.Cluster.NAT.Mode = "nstance" }},
		{"negative count", "nlb", "non-negative", func(c *ClusterConfig) { c.Cluster.NAT.FixedPublicIPv4Count = -1 }},
		{"unowned group", "nlb", "owning tenant", func(c *ClusterConfig) { c.Cluster.NAT.Groups["unused"] = NATGroup{Arch: "arm64"} }},
		{"duplicate ladder", "nlb", "duplicate", func(c *ClusterConfig) {
			n := c.Cluster.NAT.Tenants["default"]
			n.InstanceTypeLadder = []string{"small", "small"}
			c.Cluster.NAT.Tenants["default"] = n
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := networkContractConfig(t, "aws", test.exposure)
			test.change(cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
	cfg := networkContractConfig(t, "google", "nlb")
	lb := cfg.Cluster.Providers[0].LoadBalancers["api"]
	lb.Listeners[0].TargetPort = 8443
	cfg.Cluster.Providers[0].LoadBalancers["api"] = lb
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "must be equal") {
		t.Fatalf("Google accepted unequal ports: %v", err)
	}
}

// TestNATRejectsFixedSizeAndCapacityMetadata checks JSON decoding rejects unsupported NAT group fields.
func TestNATRejectsFixedSizeAndCapacityMetadata(t *testing.T) {
	for _, field := range []string{"size", "throughput", "pps"} {
		var cfg ClusterConfig
		raw := `{"cluster":{"nat":{"mode":"nstance","groups":{"nat":{"` + field + `":1}}}}}`
		if err := json.Unmarshal([]byte(raw), &cfg); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("accepted %s: %v", field, err)
		}
	}
}

// TestNATDeploymentChoices checks fixed address count and instance ladder settings.
func TestNATDeploymentChoices(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy TenantNAT
		count  int
		valid  bool
	}{
		{"fixed count", TenantNAT{}, 2, true},
		{"ladder missing starting type", TenantNAT{InstanceTypeLadder: []string{"medium", "large"}}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := networkContractConfig(t, "aws", "nlb")
			policy := test.policy
			policy.Group = "nat"
			if policy.InstanceTypeLadder == nil {
				policy.InstanceTypeLadder = []string{"small", "large"}
			}
			cfg.Cluster.NAT.Tenants["default"] = policy
			cfg.Cluster.NAT.FixedPublicIPv4Count = test.count
			if err := Validate(cfg); (err == nil) != test.valid {
				t.Fatalf("valid=%v, got %v", test.valid, err)
			}
		})
	}
}
