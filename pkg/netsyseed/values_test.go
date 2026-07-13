// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

func TestBuildPlatformComponentsValuesLocalDomain(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{Domains: []clusterconfig.Domain{{Zone: "internaltools.localhost", Provider: &clusterconfig.DomainProvider{Kind: "local"}}}}}
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
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
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
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

func TestBuildPlatformComponentsValuesSecretsProvidersEnableCSIComponents(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID:      "test-cluster",
		Domains: []clusterconfig.Domain{{Zone: "internaltools.localhost", Provider: &clusterconfig.DomainProvider{Kind: "local"}}},
		OIDC: clusterconfig.OIDC{
			IssuerURL:     "https://auth.example.com",
			ClientID:      "operator-client",
			UsernameClaim: "preferred_username",
			GroupsClaim:   "roles",
		},
		Secrets: clusterconfig.Secrets{Providers: map[string]clusterconfig.SecretsProvider{
			"aws-secrets-manager": {Kind: "aws", KeyPrefix: "shared-secrets", ObjectType: "secretsmanager"},
			"hashicorp-vault":     {Kind: "vault", CACert: "-----BEGIN CERTIFICATE-----\nvault\n-----END CERTIFICATE-----"},
			"local-fakevault":     {Kind: "openbao", CACert: "-----BEGIN CERTIFICATE-----\nlocal\n-----END CERTIFICATE-----", AuthPath: "auth/podplane", OperatorRole: "podplane-operator"},
		}},
	}}
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	crds := components["crds"].(map[string]any)
	for _, name := range []string{
		"podplane-operator-crds",
		"secrets-store-csi-driver-crds",
	} {
		crd := crds[name].(map[string]any)
		if got, want := crd["enabled"], true; got != want {
			t.Fatalf("crds.%s.enabled = %v, want %v", name, got, want)
		}
	}
	apps := components["apps"].(map[string]any)
	for _, name := range []string{
		"podplane-operator",
		"secrets-store-csi-driver",
		"secrets-store-csi-provider-aws",
		"secrets-store-csi-provider-openbao",
		"secrets-store-csi-provider-vault",
	} {
		app := apps[name].(map[string]any)
		if got, want := app["enabled"], true; got != want {
			t.Fatalf("apps.%s.enabled = %v, want %v", name, got, want)
		}
	}
	if _, ok := components["clusterID"]; ok {
		t.Fatalf("legacy platform.components.clusterID should not be set")
	}
	if _, ok := components["secrets"]; ok {
		t.Fatalf("legacy platform.components.secrets should not be set")
	}
	operator := componentValues(values, "podplane-operator")["podplane"].(map[string]any)["operator"].(map[string]any)
	config := operator["config"].(map[string]any)
	cluster := config["cluster"].(map[string]any)
	if got, want := cluster["id"], "test-cluster"; got != want {
		t.Fatalf("podplane-operator cluster id = %v, want %v", got, want)
	}
	oidc := cluster["oidc"].(map[string]any)
	for key, want := range map[string]any{
		"issuerURL":     "https://auth.example.com",
		"clientID":      "operator-client",
		"usernameClaim": "preferred_username",
		"groupsClaim":   "roles",
	} {
		if got := oidc[key]; got != want {
			t.Fatalf("podplane-operator oidc.%s = %v, want %v", key, got, want)
		}
	}
	secrets := config["secrets"].(map[string]any)
	providers := secrets["providers"].(map[string]any)
	provider := providers["aws-secrets-manager"].(map[string]any)
	if got, want := provider["keyPrefix"], "shared-secrets"; got != want {
		t.Fatalf("provider keyPrefix = %v, want %v", got, want)
	}
	localProvider := providers["local-fakevault"].(map[string]any)
	if got, want := localProvider["caCert"], "-----BEGIN CERTIFICATE-----\nlocal\n-----END CERTIFICATE-----"; got != want {
		t.Fatalf("local provider caCert = %v, want %v", got, want)
	}
	if got, want := localProvider["authPath"], "auth/podplane"; got != want {
		t.Fatalf("local provider authPath = %v, want %v", got, want)
	}
	if got, want := localProvider["operatorRole"], "podplane-operator"; got != want {
		t.Fatalf("local provider operatorRole = %v, want %v", got, want)
	}
	openBaoProvider := componentValues(values, "secrets-store-csi-provider-openbao")
	podplaneProviders := openBaoProvider["podplane"].(map[string]any)["secrets"].(map[string]any)["providers"].(map[string]any)
	localCA := podplaneProviders["local-fakevault"].(map[string]any)
	if got, want := localCA["caCert"], "-----BEGIN CERTIFICATE-----\nlocal\n-----END CERTIFICATE-----"; got != want {
		t.Fatalf("openbao provider caCert = %v, want %v", got, want)
	}
	csi := openBaoProvider["openbao"].(map[string]any)["csi"].(map[string]any)
	volumes := csi["volumes"].([]map[string]any)
	if got, want := volumes[0]["name"], "provider-ca-local-fakevault"; got != want {
		t.Fatalf("openbao csi volume name = %v, want %v", got, want)
	}
	configMap := volumes[0]["configMap"].(map[string]any)
	if got, want := configMap["name"], "podplane-secrets-provider-ca-local-fakevault"; got != want {
		t.Fatalf("openbao csi volume configMap = %v, want %v", got, want)
	}
	volumeMounts := csi["volumeMounts"].([]map[string]any)
	if got, want := volumeMounts[0]["mountPath"], "/var/run/podplane/secrets-providers/local-fakevault"; got != want {
		t.Fatalf("openbao csi volume mountPath = %v, want %v", got, want)
	}
	vaultProvider := componentValues(values, "secrets-store-csi-provider-vault")
	vaultPodplaneProviders := vaultProvider["podplane"].(map[string]any)["secrets"].(map[string]any)["providers"].(map[string]any)
	vaultCA := vaultPodplaneProviders["hashicorp-vault"].(map[string]any)
	if got, want := vaultCA["caCert"], "-----BEGIN CERTIFICATE-----\nvault\n-----END CERTIFICATE-----"; got != want {
		t.Fatalf("vault provider caCert = %v, want %v", got, want)
	}
	vaultCSI := vaultProvider["vault"].(map[string]any)["csi"].(map[string]any)
	vaultVolumes := vaultCSI["volumes"].([]map[string]any)
	vaultConfigMap := vaultVolumes[0]["configMap"].(map[string]any)
	if got, want := vaultConfigMap["name"], "podplane-secrets-provider-ca-hashicorp-vault"; got != want {
		t.Fatalf("vault csi volume configMap = %v, want %v", got, want)
	}
	vaultVolumeMounts := vaultCSI["volumeMounts"].([]map[string]any)
	if got, want := vaultVolumeMounts[0]["mountPath"], "/var/run/podplane/secrets-providers/hashicorp-vault"; got != want {
		t.Fatalf("vault csi volume mountPath = %v, want %v", got, want)
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
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	mirror := components["imageMirror"].(map[string]any)
	if got, want := mirror["enabled"], true; got != want {
		t.Fatalf("imageMirror.enabled = %v, want %v", got, want)
	}
	if got, want := mirror["hostname"], "dev-registry.local"; got != want {
		t.Fatalf("imageMirror.hostname = %v, want %v", got, want)
	}
	if got, want := mirror["prefix"], "mirror"; got != want {
		t.Fatalf("imageMirror.prefix = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesZotRegistry(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "test-cluster",
		OIDC: clusterconfig.OIDC{
			IssuerURL:     "https://auth.example.com",
			ClientID:      "registry-client",
			UsernameClaim: "preferred_username",
			GroupsClaim:   "roles",
		},
		Registry: clusterconfig.Registry{Hostname: "registry.example.com"},
		Providers: []clusterconfig.Provider{{
			Kind:    "aws",
			Account: "123456789012",
			Region:  "us-east-1",
		}},
	}}
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	zotRegistry := componentValues(values, "zot-registry")["platform"].(map[string]any)["zotRegistry"].(map[string]any)
	if got, want := zotRegistry["registryHostname"], "registry.example.com"; got != want {
		t.Fatalf("registryHostname = %v, want %v", got, want)
	}
	storage := zotRegistry["storage"].(map[string]any)
	if got, want := storage["bucket"], "test-cluster-123456789012-registry"; got != want {
		t.Fatalf("storage.bucket = %v, want %v", got, want)
	}
	if got, want := storage["region"], "us-east-1"; got != want {
		t.Fatalf("storage.region = %v, want %v", got, want)
	}
	if _, ok := storage["skipVerify"]; ok {
		t.Fatalf("storage.skipVerify should not be set for provider-backed clusters")
	}
	oidc := zotRegistry["oidc"].(map[string]any)
	if got, want := oidc["issuer"], "https://auth.example.com"; got != want {
		t.Fatalf("oidc.issuer = %v, want %v", got, want)
	}
	if got, want := oidc["audience"], "registry-client"; got != want {
		t.Fatalf("oidc.audience = %v, want %v", got, want)
	}
	if got, want := oidc["usernameClaim"], "preferred_username"; got != want {
		t.Fatalf("oidc.usernameClaim = %v, want %v", got, want)
	}
	if got, want := oidc["groupsClaim"], "roles"; got != want {
		t.Fatalf("oidc.groupsClaim = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesZotRegistryLocalBucket(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "oidc-ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID:       "dev",
		OIDC:     clusterconfig.OIDC{IssuerURL: "https://oidc.localhost", CACert: caPath},
		Registry: clusterconfig.Registry{Hostname: "dev-registry.local"},
	}}
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{ZotRegistryEndpoint: "https://10.0.2.99:19443/s3/cache"})
	if err != nil {
		t.Fatalf("buildPlatformComponentsValues error = %v", err)
	}
	zotRegistry := componentValues(values, "zot-registry")["platform"].(map[string]any)["zotRegistry"].(map[string]any)
	storage := zotRegistry["storage"].(map[string]any)
	if got, want := storage["bucket"], "registry"; got != want {
		t.Fatalf("storage.bucket = %v, want %v", got, want)
	}
	if got, want := storage["region"], "local"; got != want {
		t.Fatalf("storage.region = %v, want %v", got, want)
	}
	if got, want := storage["endpoint"], "https://10.0.2.99:19443/s3/cache"; got != want {
		t.Fatalf("storage.endpoint = %v, want %v", got, want)
	}
	if got, want := storage["secure"], true; got != want {
		t.Fatalf("storage.secure = %v, want %v", got, want)
	}
	if got, want := storage["skipVerify"], true; got != want {
		t.Fatalf("storage.skipVerify = %v, want %v", got, want)
	}
	oidc := zotRegistry["oidc"].(map[string]any)
	if got := oidc["certificateAuthority"]; got == "" {
		t.Fatal("oidc.certificateAuthority is empty, want local CA contents")
	}
	zot := componentValues(values, "zot-registry")["zot"].(map[string]any)
	hostAliases := zot["hostAliases"].([]map[string]any)
	if got, want := hostAliases[0]["ip"], "10.0.2.99"; got != want {
		t.Fatalf("hostAliases[0].ip = %v, want %v", got, want)
	}
}

func TestBuildPlatformComponentsValuesGroupsAWSSolvers(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ACME:      &clusterconfig.ACME{Server: "https://acme.example/directory", Email: "ops@example.com"},
		Providers: []clusterconfig.Provider{{Kind: "aws", Account: "123", Region: "us-east-1"}},
		Domains: []clusterconfig.Domain{
			{Zone: "example.com", Provider: &clusterconfig.DomainProvider{Kind: "aws", Account: "123", HostedZoneID: "Z123", RoleARN: "arn:aws:iam::123:role/cert-manager"}},
			{Zone: "example.net", Provider: &clusterconfig.DomainProvider{Kind: "aws", Account: "123", HostedZoneID: "Z123", RoleARN: "arn:aws:iam::123:role/cert-manager"}},
		},
	}}
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
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
		Domains: []clusterconfig.Domain{{Zone: "example.com", Provider: &clusterconfig.DomainProvider{
			Kind: "cloudflare", SecretName: "cloudflare-dns01", SecretProviderClassName: "cloudflare-dns01",
		}}},
	}}
	values, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{})
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
		Domains:   []clusterconfig.Domain{{Zone: "example.com", Provider: &clusterconfig.DomainProvider{Kind: "aws"}}},
	}}
	if _, err := buildPlatformComponentsValues(cfg, buildPlatformComponentsValuesOptions{}); err == nil {
		t.Fatalf("expected ambiguous AWS provider error")
	}
}

// componentValues returns the nested values block for a named component in the
// platform-components Helm values map.
func componentValues(values map[string]any, name string) map[string]any {
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	return components["values"].(map[string]any)[name].(map[string]any)
}
