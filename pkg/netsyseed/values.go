// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// buildPlatformComponentsValues derives platform-components Helm values from
// user-facing cluster config. The returned structure is JSON/YAML marshalable.
func buildPlatformComponentsValues(cfg *clusterconfig.ClusterConfig) (map[string]any, error) {
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
		zotStorage := map[string]any{
			"bucket": registryBucketName(cfg.Cluster),
			"region": registryRegion(cfg.Cluster),
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
		componentValues["zot-registry"] = entry
	}
	applyProviderComponents(components, cfg.Cluster.Providers)
	if err := applySecretsComponents(components, cfg); err != nil {
		return nil, err
	}
	if len(cfg.Cluster.Domains) == 0 {
		return values, nil
	}

	crds := ensureChildMap(components, "crds")
	crds["gateway-api-crds"] = map[string]any{"enabled": true}
	crds["envoy-gateway-crds"] = map[string]any{"enabled": true}
	apps := ensureChildMap(components, "apps")
	apps["envoy-gateway"] = map[string]any{"enabled": true}
	if componentValues == nil {
		componentValues = ensureChildMap(components, "values")
	}
	certificateDelivery, err := ingressCertificateDeliveryValues(cfg)
	if err != nil {
		return nil, err
	}
	componentValues["envoy-gateway"] = map[string]any{
		"platform": map[string]any{
			"envoyGateway": map[string]any{
				"ingress": map[string]any{
					"enabled":      true,
					"domains":      ingressDomains(cfg.Cluster.Domains),
					"certificates": certificateDelivery,
				},
			},
		},
	}
	return values, nil
}

// ingressCertificateDeliveryValues derives the external certificate mount
// contract consumed by the Envoy Gateway component.
func ingressCertificateDeliveryValues(cfg *clusterconfig.ClusterConfig) (map[string]any, error) {
	secrets := cfg.Cluster.Secrets
	provider, ok := secrets.Providers[secrets.DefaultProvider]
	if !ok {
		return nil, fmt.Errorf("cluster.secrets.default_provider must select a configured provider for ingress certificates")
	}
	prefix := provider.KeyPrefix
	if prefix == "" {
		prefix = cfg.Cluster.ID
	}
	value := map[string]any{"keyPrefix": prefix}
	switch {
	case provider.Kind == "aws" && provider.ObjectType == "secretsmanager":
		value["provider"] = "aws"
		value["objectType"] = "secretsmanager"
	case provider.Kind == "aws" && provider.ObjectType == "ssmparameter":
		value["provider"] = "aws"
		value["objectType"] = "ssmparameter"
	case provider.Kind == "gcp":
		value["provider"] = "gcp"
		value["projectID"] = provider.ProjectID
	case provider.Kind == "openbao":
		value["provider"] = "openbao"
		value["address"] = provider.Address
		value["mountPath"] = provider.MountPath
		authMountPath := strings.TrimPrefix(strings.Trim(provider.AuthPath, "/"), "auth/")
		if authMountPath != "" {
			value["authMountPath"] = authMountPath
		}
		if provider.CACert != "" {
			value["caCertPath"] = "/var/run/podplane/secrets-providers/" + secrets.DefaultProvider + "/ca.crt"
		}
	default:
		return nil, fmt.Errorf("cluster.secrets.default_provider %q does not support ingress certificate delivery", secrets.DefaultProvider)
	}
	return value, nil
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
func applySecretsComponents(components map[string]any, cfg *clusterconfig.ClusterConfig) error {
	secrets := cfg.Cluster.Secrets
	if len(secrets.Providers) == 0 {
		return nil
	}
	if cfg.Cluster.SPIFFE.TrustDomain != "" {
		if err := clusterconfig.ValidateWorkloadCAProvider(secrets); err != nil {
			return fmt.Errorf("cluster.secrets: %w", err)
		}
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
						"spiffe": map[string]any{
							"trustDomain": cfg.Cluster.SPIFFE.TrustDomain,
						},
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
	if len(cfg.Cluster.Domains) > 0 {
		ingressCertificates, err := ingressCertificateValues(cfg)
		if err != nil {
			return err
		}
		operator := componentValues["podplane-operator"].(map[string]any)["podplane"].(map[string]any)["operator"].(map[string]any)
		operator["config"].(map[string]any)["ingressCertificates"] = ingressCertificates
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
	return nil
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
	return map[string]any{"defaultProvider": secrets.DefaultProvider, "providers": providers}
}

// ingressCertificateValues derives proxy-neutral operator certificate configuration.
func ingressCertificateValues(cfg *clusterconfig.ClusterConfig) (map[string]any, error) {
	secrets := cfg.Cluster.Secrets
	provider := secrets.Providers[secrets.DefaultProvider]
	value := map[string]any{
		"provider":  secrets.DefaultProvider,
		"keyPrefix": provider.KeyPrefix,
		"domains":   map[string]any{},
	}
	if cfg.Cluster.ACME != nil {
		server := cfg.Cluster.ACME.Server
		if server == "" {
			server = clusterconfig.DefaultACMEServer
		}
		value["acme"] = map[string]any{"server": server, "email": cfg.Cluster.ACME.Email}
	}
	domains := value["domains"].(map[string]any)
	for _, domain := range cfg.Cluster.Domains {
		entry := map[string]any{}
		if cfg.Cluster.ACME != nil && domain.Provider.SupportsACME() {
			region, err := awsRegion(cfg, *domain.Provider)
			if err != nil {
				return nil, fmt.Errorf("domain %s: %w", domain.Zone, err)
			}
			entry["dnsProvider"] = map[string]any{
				"kind":         domain.Provider.Kind,
				"region":       region,
				"hostedZoneID": domain.Provider.HostedZoneID,
				"roleARN":      domain.Provider.RoleARN,
			}
		}
		domains[domain.Zone] = entry
	}
	return value, nil
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

// ingressDomains converts configured cluster domains into gateway ingress
// domain values and marks the first domain as the default.
func ingressDomains(domains []clusterconfig.Domain) []map[string]any {
	items := make([]map[string]any, 0, len(domains))
	for i, domain := range domains {
		item := map[string]any{"apex": domain.Zone}
		if i == 0 {
			item["default"] = true
		}
		items = append(items, item)
	}
	return items
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
