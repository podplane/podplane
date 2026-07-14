// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"fmt"
	"slices"
)

// DefaultACMEServer is the public ACME directory used when cluster.acme.server
// is omitted.
const DefaultACMEServer = "https://acme-v02.api.letsencrypt.org/directory"

// ClusterConfig represents a cluster configuration file
// Typically files are named podplane.cluster.jsonc or have
// a .cluster.jsonc suffix.
type ClusterConfig struct {
	Schema  string  `json:"$schema,omitempty"`
	Cluster Cluster `json:"cluster"`
}

// Cluster groups everything under a top-level "cluster" object,
// to assist with differentiating from a Podplane OIDC configuration
// file (which typicaly has a .oidc.json suffix)
type Cluster struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	OIDC       OIDC            `json:"oidc"`
	ACME       *ACME           `json:"acme,omitempty"`
	Domains    []Domain        `json:"domains,omitempty"`
	Registry   Registry        `json:"registry,omitempty"`
	Pools      map[string]Pool `json:"pools,omitempty"`
	Providers  []Provider      `json:"providers,omitempty"`
	Secrets    Secrets         `json:"secrets,omitempty"`
	Kubernetes Kubernetes      `json:"kubernetes"`
	Seed       Seed            `json:"seed,omitempty"`
	Components Components      `json:"components,omitempty"`
}

// Registry describes the cluster-level OCI registry endpoint used by node-local
// zot, podplane push, and optional registry ingress.
type Registry struct {
	Hostname string          `json:"hostname,omitempty"`
	Ingress  RegistryIngress `json:"ingress,omitempty"`
}

// RegistryIngress configures optional Docker-compatible public registry ingress.
type RegistryIngress struct {
	Enabled bool `json:"enabled,omitempty"`
}

// Secrets describes Podplane Secrets provider selection. Only safe provider
// metadata belongs here; backend credentials are configured on the operator.
type Secrets struct {
	DefaultProvider string                     `json:"default_provider,omitempty"`
	Providers       map[string]SecretsProvider `json:"providers,omitempty"`
}

// SecretsProvider is one named Podplane Secrets backend exposed to templates
// and CLI commands. Kind matches the upstream Secrets Store CSI provider slug.
type SecretsProvider struct {
	Kind string `json:"kind"`

	// KeyPrefix partitions backend keys for this provider. Defaults to the
	// cluster ID when omitted.
	KeyPrefix string `json:"key_prefix,omitempty"`

	// AWS ObjectType is the Secrets Store CSI AWS objectType value, such as
	// "secretsmanager" or "ssmparameter".
	ObjectType string `json:"object_type,omitempty"`
	Region     string `json:"region,omitempty"`

	// GCP fields.
	ProjectID string `json:"project_id,omitempty"`
	Location  string `json:"location,omitempty"`

	// Vault/OpenBao fields for operator/runtime configuration. These
	// fields are not persisted in cached cluster summaries.
	Address      string `json:"address,omitempty"`
	MountPath    string `json:"mount_path,omitempty"`
	CACert       string `json:"ca_cert,omitempty"`
	AuthPath     string `json:"auth_path,omitempty"`
	OperatorRole string `json:"operator_role,omitempty"`
}

// Seed describes the Podplane seed file used to create the initial Netsy snapshot.
type Seed struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// Components describes optional platform-components configuration.
type Components struct {
	Source   *ComponentsSource   `json:"source,omitempty"`
	Registry *ComponentsRegistry `json:"registry,omitempty"`
}

// ComponentsRegistry describes the registry settings platform-components should
// use when rendering component image references.
type ComponentsRegistry struct {
	Mirror ComponentsRegistryMirror `json:"mirror,omitempty"`
}

// ComponentsRegistryMirror enables explicit mirrored component image references
// such as <hostname>/<prefix>/<original-registry>/<repository>:<tag>.
type ComponentsRegistryMirror struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

// ComponentsSource overrides the Git repository used by platform-components.
type ComponentsSource struct {
	URL       string                     `json:"url"`
	Ref       ComponentsSourceRef        `json:"ref,omitempty"`
	SecretRef *ComponentsSourceSecretRef `json:"secretRef,omitempty"`
}

// ComponentsSourceRef selects a Git ref for the components source. At most one
// field should be set.
type ComponentsSourceRef struct {
	Branch string `json:"branch,omitempty"`
	Tag    string `json:"tag,omitempty"`
	Semver string `json:"semver,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// ComponentsSourceSecretRef references Flux-native Git credentials in the
// GitRepository namespace.
type ComponentsSourceSecretRef struct {
	Name string `json:"name"`
}

// ACME describes cluster-level ACME account configuration for ingress certs.
type ACME struct {
	Server string `json:"server,omitempty"`
	Email  string `json:"email"`
}

// OIDC describes the issuer the cluster's API server trusts.
type OIDC struct {
	IssuerURL     string   `json:"issuer_url"`
	ClientID      string   `json:"client_id,omitempty"`
	UsernameClaim string   `json:"username_claim,omitempty"`
	GroupsClaim   string   `json:"groups_claim,omitempty"`
	SigningAlgs   []string `json:"signing_algs,omitempty"`
	// CACert may be: an inline PEM (string starts with "-----BEGIN"), an
	// http(s):// URL, or a path on disk.
	CACert string `json:"ca_cert,omitempty"`
}

// Domain is one entry in cluster.domains.
type Domain struct {
	Zone         string          `json:"zone"`
	Provider     *DomainProvider `json:"provider,omitempty"`
	LoadBalancer string          `json:"load_balancer,omitempty"`
}

// DomainProvider is the DNS provider for a Domain.
type DomainProvider struct {
	Kind                    string `json:"kind"`
	Account                 string `json:"account,omitempty"`
	Profile                 string `json:"profile,omitempty"`
	Region                  string `json:"region,omitempty"`
	HostedZoneID            string `json:"hosted_zone_id,omitempty"`
	RoleARN                 string `json:"role_arn,omitempty"`
	SecretProviderClassName string `json:"secret_provider_class_name,omitempty"`
	SecretName              string `json:"secret_name,omitempty"`
	SecretKey               string `json:"secret_key,omitempty"`
	Project                 string `json:"project,omitempty"`
	HostedZoneName          string `json:"hosted_zone_name,omitempty"`
}

// SupportsACME reports whether Podplane currently supports automated ACME
// DNS-01 challenges for this domain provider.
func (p *DomainProvider) SupportsACME() bool {
	return p != nil && p.Kind == "aws-route53"
}

// Pool is one entry in cluster.pools.<name>.
type Pool struct {
	Arch         string `json:"arch"`
	InstanceType string `json:"instance_type"`
	Size         int    `json:"size"`
	DiskSize     int    `json:"disk_size,omitempty"`
}

// Provider is one entry in cluster.providers[].
type Provider struct {
	Kind          string                  `json:"kind"`
	Region        string                  `json:"region,omitempty"`
	Account       string                  `json:"account,omitempty"`
	Profile       string                  `json:"profile,omitempty"`
	Project       string                  `json:"project,omitempty"`
	Tags          map[string]string       `json:"tags,omitempty"`
	VPC           VPC                     `json:"vpc"`
	Zones         map[string][]Subnet     `json:"zones,omitempty"`
	LoadBalancers map[string]LoadBalancer `json:"load_balancers,omitempty"`
	Buckets       []string                `json:"buckets,omitempty"`
	Roles         map[string]Role         `json:"roles,omitempty"`
}

// VPC describes the cluster's VPC. Either ID (existing) or V4CIDR/V6CIDR
// (create new) is set, not both.
type VPC struct {
	ID     string `json:"id,omitempty"`
	V4CIDR string `json:"v4cidr,omitempty"`
	V6CIDR string `json:"v6cidr,omitempty"`
}

// Subnet is one entry in cluster.providers[].zones.<zone>[].
type Subnet struct {
	Pool     string   `json:"pool,omitempty"`
	Services []string `json:"services,omitempty"`
	Public   bool     `json:"public,omitempty"`
	ID       string   `json:"id,omitempty"`
	V4CIDR   string   `json:"v4cidr,omitempty"`
	V6CIDR   string   `json:"v6cidr,omitempty"`
}

// ResolvedRole returns the subnet role used by the Nstance network module.
func (s Subnet) ResolvedRole() string {
	if s.Pool != "" {
		return s.Pool
	}
	if slices.Contains(s.Services, "nstance") {
		return "nstance"
	}
	if s.Public && (slices.Contains(s.Services, "nat") || slices.Contains(s.Services, "nlb")) {
		return "public"
	}
	return "services"
}

// LoadBalancer describes one provider load balancer.
type LoadBalancer struct {
	Public    bool       `json:"public"`
	Subnets   string     `json:"subnets"`
	Listeners []Listener `json:"listeners"`
}

// Listener describes one load-balancer listener and its target pool.
type Listener struct {
	Port       int    `json:"port"`
	TargetPort int    `json:"target_port,omitempty"`
	Pool       string `json:"pool"`
}

// Role is one entry in cluster.providers[].roles.<name>.
type Role struct {
	Buckets     []string `json:"buckets"`
	Permissions string   `json:"permissions,omitempty"`
}

// Kubernetes describes how the API server is reached and configured.
type Kubernetes struct {
	APIHostname     string   `json:"api_hostname"`
	APIPort         int      `json:"api_port,omitempty"`
	APILoadBalancer string   `json:"api_load_balancer,omitempty"`
	ClusterCIDR     []string `json:"cluster_cidr,omitempty"`
	ServiceCIDR     []string `json:"service_cidr,omitempty"`
}

// DefaultDomain returns the first configured domain, or nil when the cluster
// has no domains.
func (c *ClusterConfig) DefaultDomain() *Domain {
	if len(c.Cluster.Domains) == 0 {
		return nil
	}
	return &c.Cluster.Domains[0]
}

// ResolvedDomainLoadBalancer returns the domain's selected load balancer.
func (d Domain) ResolvedDomainLoadBalancer() string {
	if d.LoadBalancer != "" {
		return d.LoadBalancer
	}
	return "main"
}

// ResolvedRegistryHostname returns the configured registry hostname or the
// stable domainless hostname.
func (c *ClusterConfig) ResolvedRegistryHostname() string {
	if c.Cluster.Registry.Hostname != "" {
		return c.Cluster.Registry.Hostname
	}
	if len(c.Cluster.Domains) > 0 {
		return ""
	}
	return c.Cluster.ID + "-registry.local"
}

// ResolvedClientID returns the configured OIDC client_id, defaulting to the
// cluster ID.
func (c *ClusterConfig) ResolvedClientID() string {
	if c.Cluster.OIDC.ClientID != "" {
		return c.Cluster.OIDC.ClientID
	}
	return c.Cluster.ID
}

// ResolvedUsernameClaim returns the configured username_claim, defaulting to
// "email".
func (c *ClusterConfig) ResolvedUsernameClaim() string {
	if c.Cluster.OIDC.UsernameClaim != "" {
		return c.Cluster.OIDC.UsernameClaim
	}
	return "email"
}

// ResolvedGroupsClaim returns the configured groups_claim, defaulting to
// "groups".
func (c *ClusterConfig) ResolvedGroupsClaim() string {
	if c.Cluster.OIDC.GroupsClaim != "" {
		return c.Cluster.OIDC.GroupsClaim
	}
	return "groups"
}

// ResolvedKubernetesAPIURL builds the https URL for the cluster's API server.
// Defaults to port 6443 if api_port is unset. Returns "" if api_hostname is
// not set.
func (c *ClusterConfig) ResolvedKubernetesAPIURL() string {
	host := c.Cluster.Kubernetes.APIHostname
	if host == "" {
		return ""
	}
	port := c.Cluster.Kubernetes.APIPort
	if port == 0 {
		port = 6443
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}
