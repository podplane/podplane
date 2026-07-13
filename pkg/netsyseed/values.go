// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/podplane/podplane/internal/clusterconfig"
)

const (
	acmeIssuerName       = "platform-ingress-acme-clusterissuer"
	selfsignedIssuerName = "platform-ingress-selfsigned-clusterissuer"
)

// buildPlatformComponentsValuesOptions carries runtime-only inputs that should
// not be persisted in cluster config.
type buildPlatformComponentsValuesOptions struct {
	ZotRegistryEndpoint string
}

// buildPlatformComponentsValues derives platform-components Helm values from
// user-facing cluster config. The returned structure is JSON/YAML marshalable.
func buildPlatformComponentsValues(cfg *clusterconfig.ClusterConfig, opts buildPlatformComponentsValuesOptions) (map[string]any, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cluster config is required")
	}
	values := map[string]any{
		"platform": map[string]any{
			"components": map[string]any{},
		},
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	var componentValues map[string]any
	if cfg.Cluster.Components.Registry != nil && cfg.Cluster.Components.Registry.Mirror.Enabled {
		applyRegistryMirror(components, cfg.Cluster)
	}
	registryHostname := cfg.Cluster.Registry.Hostname
	if registryHostname == "" && len(cfg.Cluster.Providers) > 0 {
		registryHostname = cfg.ResolvedRegistryHostname()
	}
	if registryHostname != "" {
		componentValues = ensureChildMap(components, "values")
		isLocal := len(cfg.Cluster.Providers) == 0
		zotStorage := map[string]any{
			"bucket": registryBucketName(cfg.Cluster),
			"region": registryRegion(cfg.Cluster),
		}
		if isLocal {
			if opts.ZotRegistryEndpoint == "" {
				return nil, fmt.Errorf("zot registry endpoint is required for local registry seed values")
			}
			zotStorage["endpoint"] = opts.ZotRegistryEndpoint
			zotStorage["secure"] = true
			zotStorage["skipVerify"] = true
			zotStorage["forcePathStyle"] = true
			zotStorage["accessKeyID"] = "test"
			zotStorage["secretAccessKey"] = "test"
		}
		zotOIDC := map[string]any{
			"issuer":        cfg.Cluster.OIDC.IssuerURL,
			"audience":      cfg.ResolvedClientID(),
			"usernameClaim": cfg.ResolvedUsernameClaim(),
			"groupsClaim":   cfg.ResolvedGroupsClaim(),
		}
		if ca := cfg.Cluster.OIDC.CACert; strings.HasPrefix(strings.TrimSpace(ca), "-----BEGIN") {
			zotOIDC["certificateAuthority"] = ca
		} else if ca != "" && !strings.Contains(ca, "://") {
			if contents, err := os.ReadFile(ca); err == nil {
				zotOIDC["certificateAuthority"] = string(contents)
			}
		}
		entry := map[string]any{
			"platform": map[string]any{
				"zotRegistry": map[string]any{
					"registryHostname": registryHostname,
					"storage":          zotStorage,
					"oidc":             zotOIDC,
				},
			},
		}
		if isLocal {
			endpoint, err := url.Parse(opts.ZotRegistryEndpoint)
			if err != nil || endpoint.Hostname() == "" {
				return nil, fmt.Errorf("invalid zot registry endpoint %q", opts.ZotRegistryEndpoint)
			}
			entry["zot"] = map[string]any{
				"hostAliases": []map[string]any{{
					"ip":        endpoint.Hostname(),
					"hostnames": []string{"oidc.localhost"},
				}},
			}
		}
		componentValues["zot-registry"] = entry
	}
	applyProviderComponents(components, cfg.Cluster.Providers)
	applySecretsComponents(components, cfg)
	if len(cfg.Cluster.Domains) == 0 {
		return values, nil
	}

	issuerName := selfsignedIssuerName
	platformCerts := map[string]any{
		"platform": map[string]any{
			"certs": map[string]any{},
		},
	}
	if cfg.Cluster.ACME != nil {
		issuerName = acmeIssuerName
		acme, err := acmeValues(cfg)
		if err != nil {
			return nil, err
		}
		platformCerts["platform"].(map[string]any)["certs"].(map[string]any)["ingress"] = map[string]any{
			"acme": acme,
		}
	}

	crds := ensureChildMap(components, "crds")
	crds["cert-manager-crds"] = map[string]any{"enabled": true}
	crds["traefik-crds"] = map[string]any{"enabled": true}
	crds["gateway-api-crds"] = map[string]any{"enabled": true}
	apps := ensureChildMap(components, "apps")
	apps["cert-manager"] = map[string]any{"enabled": true}
	apps["platform-certs"] = map[string]any{"enabled": true}
	apps["traefik"] = map[string]any{"enabled": true}
	if componentValues == nil {
		componentValues = ensureChildMap(components, "values")
	}
	componentValues["platform-certs"] = platformCerts
	componentValues["traefik"] = map[string]any{
		"platform": map[string]any{
			"traefik": map[string]any{
				"ingress": map[string]any{
					"enabled": true,
					"issuerRef": map[string]any{
						"kind": "ClusterIssuer",
						"name": issuerName,
					},
					"domains": ingressDomains(cfg.Cluster.Domains),
				},
			},
		},
	}
	if secretSync := secretSyncValues(cfg.Cluster.Domains); secretSync != nil {
		platformCerts["platform"].(map[string]any)["certs"].(map[string]any)["secretSync"] = secretSync
	}
	return values, nil
}

// registryBucketName returns the backing bucket name used by the in-cluster
// zot-registry component. Local clusters use the fake-S3 bucket named
// "registry"; AWS clusters use the same account-qualified name as generated
// Terraform when the account is known from cluster config.
func registryBucketName(cluster clusterconfig.Cluster) string {
	if len(cluster.Providers) == 0 {
		return "registry"
	}
	for _, provider := range cluster.Providers {
		if provider.Kind == "aws" && provider.Account != "" {
			return fmt.Sprintf("%s-%s-registry", cluster.ID, provider.Account)
		}
	}
	return cluster.ID + "-registry"
}

// registryRegion returns the object-storage region for the in-cluster
// zot-registry component, defaulting to the chart's local development region
// when the cluster config does not include a cloud provider.
func registryRegion(cluster clusterconfig.Cluster) string {
	if len(cluster.Providers) == 0 {
		return "local"
	}
	for _, provider := range cluster.Providers {
		if provider.Kind == "aws" && provider.Region != "" {
			return provider.Region
		}
	}
	return "local"
}

// applyRegistryMirror configures platform-components to render explicit image
// references to the configured component image mirror.
func applyRegistryMirror(components map[string]any, cluster clusterconfig.Cluster) {
	if cluster.Components.Registry == nil || !cluster.Components.Registry.Mirror.Enabled {
		return
	}
	components["imageMirror"] = map[string]any{
		"enabled":  true,
		"hostname": cluster.RegistryMirrorHostname(),
		"prefix":   cluster.RegistryMirrorPrefix(),
	}
}

// applyProviderComponents enables provider-specific core components required by
// the configured infrastructure providers.
func applyProviderComponents(components map[string]any, providers []clusterconfig.Provider) {
	apps := ensureChildMap(components, "apps")
	for _, provider := range providers {
		switch provider.Kind {
		case "aws":
			apps["csi-aws-ebs"] = map[string]any{"enabled": true}
		}
	}
}

// applySecretsComponents enables Secrets Store CSI Driver, provider components,
// and Podplane operator provider configuration required by configured Podplane
// Secrets providers.
func applySecretsComponents(components map[string]any, cfg *clusterconfig.ClusterConfig) {
	secrets := cfg.Cluster.Secrets
	if len(secrets.Providers) == 0 {
		return
	}
	crds := ensureChildMap(components, "crds")
	crds["podplane-operator-crds"] = map[string]any{"enabled": true}
	crds["secrets-store-csi-driver-crds"] = map[string]any{"enabled": true}
	apps := ensureChildMap(components, "apps")
	apps["podplane-operator"] = map[string]any{"enabled": true}
	apps["secrets-store-csi-driver"] = map[string]any{"enabled": true}
	componentValues := ensureChildMap(components, "values")
	componentValues["podplane-operator"] = map[string]any{
		"podplane": map[string]any{
			"operator": map[string]any{
				"config": map[string]any{
					"cluster": map[string]any{
						"id": cfg.Cluster.ID,
						"oidc": map[string]any{
							"issuerURL":     cfg.Cluster.OIDC.IssuerURL,
							"clientID":      cfg.ResolvedClientID(),
							"usernameClaim": cfg.ResolvedUsernameClaim(),
							"groupsClaim":   cfg.ResolvedGroupsClaim(),
						},
					},
					"secrets": secretsValues(secrets),
				},
			},
		},
	}
	names := make([]string, 0, len(secrets.Providers))
	for name := range secrets.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		provider := secrets.Providers[name]
		switch provider.Kind {
		case "aws":
			apps["secrets-store-csi-provider-aws"] = map[string]any{"enabled": true}
		case "gcp":
			apps["secrets-store-csi-provider-gcp"] = map[string]any{"enabled": true}
		case "vault":
			apps["secrets-store-csi-provider-vault"] = map[string]any{"enabled": true}
			applyVaultLikeCSIProviderCAValues(componentValues, "secrets-store-csi-provider-vault", "vault", name, provider)
		case "openbao":
			apps["secrets-store-csi-provider-openbao"] = map[string]any{"enabled": true}
			applyVaultLikeCSIProviderCAValues(componentValues, "secrets-store-csi-provider-openbao", "openbao", name, provider)
		}
	}
}

// secretsValues converts cluster secrets config to platform-components values.
func secretsValues(secrets clusterconfig.Secrets) map[string]any {
	providers := make(map[string]any, len(secrets.Providers))
	names := make([]string, 0, len(secrets.Providers))
	for name := range secrets.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		provider := secrets.Providers[name]
		entry := map[string]any{"kind": provider.Kind}
		setIfNotEmpty(entry, "keyPrefix", provider.KeyPrefix)
		setIfNotEmpty(entry, "objectType", provider.ObjectType)
		setIfNotEmpty(entry, "region", provider.Region)
		setIfNotEmpty(entry, "projectID", provider.ProjectID)
		setIfNotEmpty(entry, "location", provider.Location)
		setIfNotEmpty(entry, "address", provider.Address)
		setIfNotEmpty(entry, "mountPath", provider.MountPath)
		setIfNotEmpty(entry, "caCert", provider.CACert)
		setIfNotEmpty(entry, "authPath", provider.AuthPath)
		setIfNotEmpty(entry, "operatorRole", provider.OperatorRole)
		providers[name] = entry
	}
	return map[string]any{"providers": providers}
}

// applyVaultLikeCSIProviderCAValues configures a Vault/OpenBao CSI provider
// chart to render and mount a provider-specific CA bundle.
func applyVaultLikeCSIProviderCAValues(componentValues map[string]any, componentName, chartRoot, providerName string, provider clusterconfig.SecretsProvider) {
	if provider.CACert == "" {
		return
	}
	entry := ensureChildMap(componentValues, componentName)
	podplane := ensureChildMap(entry, "podplane")
	secrets := ensureChildMap(podplane, "secrets")
	providers := ensureChildMap(secrets, "providers")
	providers[providerName] = map[string]any{"caCert": provider.CACert}

	chart := ensureChildMap(entry, chartRoot)
	csi := ensureChildMap(chart, "csi")
	volumes, _ := csi["volumes"].([]map[string]any)
	volumeMounts, _ := csi["volumeMounts"].([]map[string]any)
	volumeName := "provider-ca-" + providerName
	csi["volumes"] = append(volumes, map[string]any{
		"name": volumeName,
		"configMap": map[string]any{
			"name": "podplane-secrets-provider-ca-" + providerName,
		},
	})
	csi["volumeMounts"] = append(volumeMounts, map[string]any{
		"name":      volumeName,
		"mountPath": "/var/run/podplane/secrets-providers/" + providerName,
		"readOnly":  true,
	})
}

// setIfNotEmpty stores a string value in m when value is not empty.
func setIfNotEmpty(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// ensureChildMap returns the existing child map for key, or creates and stores
// an empty map when the key is absent or not already a map.
func ensureChildMap(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// ingressDomains converts configured cluster domains into Traefik ingress
// domain values and marks the first domain as the default.
func ingressDomains(domains []clusterconfig.Domain) []map[string]any {
	items := make([]map[string]any, 0, len(domains))
	for i, domain := range domains {
		item := map[string]any{"zone": domain.Zone}
		if i == 0 {
			item["default"] = true
		}
		items = append(items, item)
	}
	return items
}

// acmeValues derives platform-certs ACME values from the cluster ACME and
// domain provider configuration.
func acmeValues(cfg *clusterconfig.ClusterConfig) (map[string]any, error) {
	if cfg.Cluster.ACME.Server == "" {
		return nil, fmt.Errorf("cluster.acme.server is required when cluster.acme is configured")
	}
	if cfg.Cluster.ACME.Email == "" {
		return nil, fmt.Errorf("cluster.acme.email is required when cluster.acme is configured")
	}
	solvers, err := acmeSolvers(cfg)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":    true,
		"issuerName": acmeIssuerName,
		"server":     cfg.Cluster.ACME.Server,
		"email":      cfg.Cluster.ACME.Email,
		"solvers":    solvers,
	}, nil
}

// acmeSolvers groups configured domains by equivalent DNS-01 provider settings
// and returns deterministic cert-manager ACME solver values.
func acmeSolvers(cfg *clusterconfig.ClusterConfig) ([]map[string]any, error) {
	type solverGroup struct {
		zones []string
		value map[string]any
	}
	groups := map[string]*solverGroup{}
	for _, domain := range cfg.Cluster.Domains {
		provider := domain.Provider
		if provider == nil || provider.Kind == "" {
			return nil, fmt.Errorf("cluster.domains[] provider.kind is required for %s", domain.Zone)
		}
		providerValue, key, err := dnsProviderSolver(cfg, *provider)
		if err != nil {
			return nil, fmt.Errorf("domain %s: %w", domain.Zone, err)
		}
		group, ok := groups[key]
		if !ok {
			group = &solverGroup{value: providerValue}
			groups[key] = group
		}
		group.zones = append(group.zones, domain.Zone)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	solvers := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sort.Strings(group.zones)
		solver := map[string]any{"dnsZones": group.zones}
		for name, value := range group.value {
			solver[name] = value
		}
		solvers = append(solvers, solver)
	}
	return solvers, nil
}

// dnsProviderSolver converts one domain provider into a cert-manager DNS-01
// solver value and a stable grouping key.
func dnsProviderSolver(cfg *clusterconfig.ClusterConfig, provider clusterconfig.DomainProvider) (map[string]any, string, error) {
	switch provider.Kind {
	case "aws":
		region, err := awsRegion(cfg, provider)
		if err != nil {
			return nil, "", err
		}
		route53 := map[string]any{}
		if region != "" {
			route53["region"] = region
		}
		if provider.HostedZoneID != "" {
			route53["hostedZoneID"] = provider.HostedZoneID
		}
		if provider.RoleARN != "" {
			route53["roleArn"] = provider.RoleARN
		}
		return map[string]any{"route53": route53}, "aws|" + region + "|" + provider.HostedZoneID + "|" + provider.RoleARN, nil
	case "cloudflare":
		if provider.SecretName == "" {
			return nil, "", fmt.Errorf("provider.secret_name is required for cloudflare DNS-01")
		}
		key := provider.SecretKey
		if key == "" {
			key = "api-token"
		}
		cloudflare := map[string]any{"apiTokenSecretRef": map[string]any{"name": provider.SecretName, "key": key}}
		return map[string]any{"cloudflare": cloudflare}, "cloudflare|" + provider.SecretName + "|" + key, nil
	case "google":
		if provider.Project == "" {
			return nil, "", fmt.Errorf("provider.project is required for google DNS-01")
		}
		cloudDNS := map[string]any{"project": provider.Project}
		if provider.HostedZoneName != "" {
			cloudDNS["hostedZoneName"] = provider.HostedZoneName
		}
		if provider.SecretName != "" {
			key := provider.SecretKey
			if key == "" {
				key = "service-account.json"
			}
			cloudDNS["serviceAccountSecretRef"] = map[string]any{"name": provider.SecretName, "key": key}
		}
		return map[string]any{"cloudDNS": cloudDNS}, "google|" + provider.Project + "|" + provider.HostedZoneName + "|" + provider.SecretName + "|" + provider.SecretKey, nil
	default:
		return nil, "", fmt.Errorf("unsupported DNS provider kind %q", provider.Kind)
	}
}

// awsRegion resolves the AWS region for a DNS provider from either the domain
// provider itself or a single matching cluster-level AWS provider.
func awsRegion(cfg *clusterconfig.ClusterConfig, provider clusterconfig.DomainProvider) (string, error) {
	if provider.Region != "" {
		return provider.Region, nil
	}
	var matches []clusterconfig.Provider
	for _, p := range cfg.Cluster.Providers {
		if p.Kind != "aws" {
			continue
		}
		if provider.Account != "" && p.Account != provider.Account {
			continue
		}
		if provider.Profile != "" && p.Profile != provider.Profile {
			continue
		}
		matches = append(matches, p)
	}
	if len(matches) == 1 {
		return matches[0].Region, nil
	}
	if len(matches) == 0 {
		return "", nil
	}
	return "", fmt.Errorf("provider.region is required when multiple AWS providers match")
}

// secretSyncValues derives platform-certs secret sync values for unique Secret
// Store CSI Driver provider classes referenced by configured domains.
func secretSyncValues(domains []clusterconfig.Domain) map[string]any {
	seen := map[string]bool{}
	mounts := []map[string]any{}
	for _, domain := range domains {
		if domain.Provider == nil {
			continue
		}
		spc := domain.Provider.SecretProviderClassName
		if spc == "" || seen[spc] {
			continue
		}
		seen[spc] = true
		mounts = append(mounts, map[string]any{
			"name":                    secretSyncMountName(spc),
			"secretProviderClassName": spc,
			"mountPath":               "/mnt/secrets-store/" + secretSyncMountName(spc),
		})
	}
	if len(mounts) == 0 {
		return nil
	}
	return map[string]any{"enabled": true, "mounts": mounts}
}

// secretSyncMountName converts a SecretProviderClass name into a safe mount name.
func secretSyncMountName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastHyphen := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}
