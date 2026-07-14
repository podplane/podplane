// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestValidateComponentsRejectsMultipleSourceRefs(t *testing.T) {
	err := ValidateComponents(Components{Source: &ComponentsSource{Ref: ComponentsSourceRef{Branch: "main", Tag: "v1.0.0"}}})
	if err == nil {
		t.Fatal("ValidateComponents returned nil, want error")
	}
}

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

func TestValidateSeedDigest(t *testing.T) {
	if err := ValidateSeed(Seed{Name: "recommended", Digest: "sha512:" + strings.Repeat("a", 128)}); err != nil {
		t.Fatalf("ValidateSeed returned error for valid digest: %v", err)
	}
	if err := ValidateSeed(Seed{Name: "recommended"}); err == nil {
		t.Fatal("ValidateSeed returned nil without required digest")
	}
	if err := ValidateSeed(Seed{Name: "recommended", Digest: "sha512:invalid"}); err == nil {
		t.Fatal("ValidateSeed returned nil for invalid digest")
	}
}

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
		Listeners: []Listener{{Port: 443, TargetPort: 6443, Pool: "control-plane"}},
	}
	cfg.Cluster.Kubernetes.APILoadBalancer = "kubernetes-api"
	cfg.Cluster.Kubernetes.APIPort = 443
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate separate port-443 API load balancer returned error: %v", err)
	}

	cfg = validManagedConfig()
	cfg.Cluster.Providers[0].LoadBalancers["kubernetes-api"] = LoadBalancer{
		Public: true, Subnets: "public",
		Listeners: []Listener{{Port: 6443, Pool: "control-plane"}},
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
