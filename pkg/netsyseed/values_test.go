// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

func TestBuildPlatformComponentsValuesLocalDomain(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{Domains: []clusterconfig.Domain{{Zone: "internaltools.localhost", Provider: clusterconfig.DomainProvider{Kind: "local"}}}}}
	values, err := buildPlatformComponentsValues(cfg)
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	ingress := componentValues(values, "traefik")["platform"].(map[string]any)["traefik"].(map[string]any)["ingress"].(map[string]any)
	issuer := ingress["issuerRef"].(map[string]any)
	if got, want := issuer["name"], "platform-ingress-selfsigned-clusterissuer"; got != want {
		t.Fatalf("issuerRef.name = %v, want %v", got, want)
	}
	domains := ingress["domains"].([]map[string]any)
	if got, want := domains[0]["zone"], "internaltools.localhost"; got != want {
		t.Fatalf("domain zone = %v, want %v", got, want)
	}
	if got, want := domains[0]["default"], true; got != want {
		t.Fatalf("domain default = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesAWSProviderEnablesEBSCSI(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		Providers: []clusterconfig.Provider{{Kind: "aws"}},
	}}
	values, err := buildPlatformComponentsValues(cfg)
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	apps := components["apps"].(map[string]any)
	app := apps["csi-aws-ebs"].(map[string]any)
	if got, want := app["enabled"], true; got != want {
		t.Fatalf("apps.csi-aws-ebs.enabled = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesRegistryMirror(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		Components: clusterconfig.Components{
			Registry: &clusterconfig.ComponentsRegistry{
				Mirror: clusterconfig.ComponentsRegistryMirror{Enabled: true, Hostname: "dev-registry.local"},
			},
		},
	}}
	values, err := buildPlatformComponentsValues(cfg)
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	mirror := components["registry"].(map[string]any)["mirror"].(map[string]any)
	if got, want := mirror["enabled"], true; got != want {
		t.Fatalf("registry.mirror.enabled = %v, want %v", got, want)
	}
	if got, want := mirror["hostname"], "dev-registry.local"; got != want {
		t.Fatalf("registry.mirror.hostname = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesGroupsAWSSolvers(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ACME:      &clusterconfig.ACME{Server: "https://acme.example/directory", Email: "ops@example.com"},
		Providers: []clusterconfig.Provider{{Kind: "aws", Account: "123", Region: "us-east-1"}},
		Domains: []clusterconfig.Domain{
			{Zone: "example.com", Provider: clusterconfig.DomainProvider{Kind: "aws", Account: "123", HostedZoneID: "Z123", RoleARN: "arn:aws:iam::123:role/cert-manager"}},
			{Zone: "example.net", Provider: clusterconfig.DomainProvider{Kind: "aws", Account: "123", HostedZoneID: "Z123", RoleARN: "arn:aws:iam::123:role/cert-manager"}},
		},
	}}
	values, err := buildPlatformComponentsValues(cfg)
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	certs := componentValues(values, "platform-certs")["platform"].(map[string]any)["certs"].(map[string]any)
	acme := certs["ingress"].(map[string]any)["acme"].(map[string]any)
	solvers := acme["solvers"].([]map[string]any)
	if got, want := len(solvers), 1; got != want {
		t.Fatalf("solver count = %d, want %d", got, want)
	}
	zones := solvers[0]["dnsZones"].([]string)
	if got, want := len(zones), 2; got != want {
		t.Fatalf("dnsZones count = %d, want %d", got, want)
	}
	route53 := solvers[0]["route53"].(map[string]any)
	if got, want := route53["region"], "us-east-1"; got != want {
		t.Fatalf("route53.region = %v, want %v", got, want)
	}
	if got, want := route53["hostedZoneID"], "Z123"; got != want {
		t.Fatalf("route53.hostedZoneID = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesCloudflareSecretSync(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ACME: &clusterconfig.ACME{Server: "https://acme.example/directory", Email: "ops@example.com"},
		Domains: []clusterconfig.Domain{{Zone: "example.com", Provider: clusterconfig.DomainProvider{
			Kind: "cloudflare", SecretName: "cloudflare-dns01", SecretProviderClassName: "cloudflare-dns01",
		}}},
	}}
	values, err := buildPlatformComponentsValues(cfg)
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	certs := componentValues(values, "platform-certs")["platform"].(map[string]any)["certs"].(map[string]any)
	if certs["secretSync"] == nil {
		t.Fatalf("expected secretSync values")
	}
	solver := certs["ingress"].(map[string]any)["acme"].(map[string]any)["solvers"].([]map[string]any)[0]
	ref := solver["cloudflare"].(map[string]any)["apiTokenSecretRef"].(map[string]any)
	if got, want := ref["name"], "cloudflare-dns01"; got != want {
		t.Fatalf("cloudflare secret name = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesAmbiguousAWSRegion(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ACME:      &clusterconfig.ACME{Server: "https://acme.example/directory", Email: "ops@example.com"},
		Providers: []clusterconfig.Provider{{Kind: "aws", Region: "us-east-1"}, {Kind: "aws", Region: "us-west-2"}},
		Domains:   []clusterconfig.Domain{{Zone: "example.com", Provider: clusterconfig.DomainProvider{Kind: "aws"}}},
	}}
	if _, err := buildPlatformComponentsValues(cfg); err == nil {
		t.Fatalf("expected ambiguous AWS provider error")
	}
}

// componentValues returns the nested values block for a named component in the
// platform-components Helm values map.
func componentValues(values map[string]any, name string) map[string]any {
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	return components["values"].(map[string]any)[name].(map[string]any)
}
