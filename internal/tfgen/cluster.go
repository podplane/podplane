// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/podplane/podplane/internal/buildvars"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterspec"
	"github.com/podplane/podplane/pkg/seeds"
)

const (
	// PodplaneProviderSource is the registry address of the Podplane provider.
	PodplaneProviderSource = "podplane/podplane"
	// NstanceModuleVersion is the supported Nstance module version constraint.
	NstanceModuleVersion = "~> 2.1"
)

// PodplaneProviderVersion selects the provider built with this CLI release.
// Development overrides replace the installed provider at runtime; init still
// needs a published provider selection, without requiring a new release.
func PodplaneProviderVersion() string {
	version := buildvars.ProviderVersion()
	if version == "0.0.0-dev" {
		return ">= 0.0.0"
	}
	return "= " + version
}

// ClusterOptions provides dependency inputs needed to render cluster Terraform.
type ClusterOptions struct {
	DepsMirrorURL     string
	VMConfigManifests []VMConfigManifest
	NstanceModuleDir  string
}

// VMConfigManifest is one pinned vmconfig manifest used by generated Terraform.
type VMConfigManifest struct {
	Kind     string
	Arch     string
	Filename string
	JSON     []byte
}

// VMTarget identifies a vmconfig kind and architecture required by a cluster.
type VMTarget struct {
	Kind string
	Arch string
}

// dataName returns this target's Terraform-safe data source name.
func (t VMTarget) dataName() string {
	return safeName(t.Kind, t.Arch)
}

// GenerateCluster renders managed Terraform files for a cluster config path.
func GenerateCluster(configPath string, cfg *clusterconfig.ClusterConfig, opts ClusterOptions) ([]File, error) {
	if opts.NstanceModuleDir == "" {
		return nil, fmt.Errorf("nstance module source directory is required")
	}
	if err := ValidateClusterGeneration(cfg); err != nil {
		return nil, err
	}
	network, err := clusterconfig.ServiceNetworkFromCIDRs(cfg.Cluster.Kubernetes.ServiceCIDR)
	if err != nil {
		return nil, err
	}
	provider := cfg.Cluster.Providers[0]
	return renderCluster(configPath, cfg, provider, opts, network)
}

// ValidateClusterGeneration checks that the generator can represent the config
// before dependency downloads or managed-file writes.
func ValidateClusterGeneration(cfg *clusterconfig.ClusterConfig) error {
	if err := clusterconfig.Validate(cfg); err != nil {
		return err
	}
	if err := clusterconfig.ValidateWorkloadCAProvider(cfg.Cluster.Secrets); err != nil {
		return fmt.Errorf("cluster.secrets: %w", err)
	}
	provider := cfg.Cluster.Providers[0]
	if len(cfg.Cluster.Providers) != 1 || (provider.Kind != "aws" && provider.Kind != "google") {
		return fmt.Errorf("cluster generation requires exactly one AWS or Google provider")
	}
	for name, lb := range provider.LoadBalancers {
		for _, listener := range lb.Listeners {
			if listener.Pool != lb.Listeners[0].Pool {
				return fmt.Errorf("load balancer %q listeners must share one pool", name)
			}
		}
	}
	if provider.Kind == "google" {
		if err := googleGenerationError(cfg); err != nil {
			return err
		}
	}
	return validateDNSProviders(cfg)
}

// validateDNSProviders rejects DNS providers that managed Terraform generation
// does not implement.
func validateDNSProviders(cfg *clusterconfig.ClusterConfig) error {
	if cfg.Cluster.Tunnel != nil {
		return nil // Existing tunnels include user-managed DNS and edge settings.
	}
	kind := "aws-route53"
	if cfg.Cluster.Providers[0].Kind == "google" {
		kind = "google-cloud-dns"
	}
	for i, domain := range cfg.Cluster.Domains {
		if domain.Provider != nil && domain.Provider.Kind != kind {
			return fmt.Errorf("cluster.domains[%d].provider.kind %q is not supported by Terraform generation", i, domain.Provider.Kind)
		}
	}
	return nil
}

// WriteCluster writes managed Terraform files for a cluster config path.
func WriteCluster(configPath string, cfg *clusterconfig.ClusterConfig, opts ClusterOptions) error {
	files, err := GenerateCluster(configPath, cfg, opts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(configPath)
	if err := WriteFiles(dir, files); err != nil {
		return err
	}
	return clusterconfig.WriteSchema(dir)
}

// TerraformDependencyRoot renders a minimal root module containing all cluster
// providers and modules so their complete dependency graph can be resolved.
// An empty moduleVersion uses the normal version constraint.
func TerraformDependencyRoot(moduleVersion string) string {
	var doc hclDocument
	addClusterTerraformRequirements(&doc, "aws", "google")
	if moduleVersion == "" {
		moduleVersion = NstanceModuleVersion
	}
	for _, name := range []string{"cluster", "account", "network", "shard"} {
		for _, provider := range []struct{ kind, prefix string }{
			{kind: "aws"},
			{kind: "google", prefix: "google_"},
		} {
			module := block("module", provider.prefix+name)
			module.Body.Attr("source", str("nstance-dev/nstance/"+provider.kind+"//modules/"+name))
			module.Body.Attr("version", str(moduleVersion))
			doc.AddBlock(module)
		}
	}
	return doc.String()
}

// addClusterTerraformRequirements adds engine, cloud provider, and Podplane constraints.
func addClusterTerraformRequirements(doc *hclDocument, cloudProviders ...string) {
	terraform := block("terraform")
	terraform.Body.Attr("required_version", str(">= 1.6.0"))
	requiredProviders := block("required_providers")
	if slices.Contains(cloudProviders, "aws") {
		requiredProviders.Body.Attr("aws", object(
			identField("source", str(AWSProviderSource)),
			identField("version", str(AWSProviderVersion)),
		))
	}
	if slices.Contains(cloudProviders, "google") {
		requiredProviders.Body.Attr("google", object(
			identField("source", str("hashicorp/google")),
			identField("version", str(">= 6.0")),
		))
	}
	requiredProviders.Body.Attr("podplane", object(
		identField("source", str(PodplaneProviderSource)),
		identField("version", str(PodplaneProviderVersion())),
	))
	terraform.Body.Block(requiredProviders)
	doc.AddBlock(terraform)
}

// nstanceModuleSource returns the local source for a generated Nstance submodule.
func nstanceModuleSource(opts ClusterOptions, name string) string {
	return strings.TrimSuffix(filepath.ToSlash(opts.NstanceModuleDir), "/") + "/modules/" + name
}

// addNstanceModuleSource adds a local Nstance submodule source to a module block.
func addNstanceModuleSource(module *hclBlock, opts ClusterOptions, name string) {
	module.Body.Attr("source", str(nstanceModuleSource(opts, name)))
}

// renderCluster renders the shared cluster stack and its cloud-specific resources.
func renderCluster(configPath string, cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider, opts ClusterOptions, serviceNetwork clusterconfig.ServiceNetwork) ([]File, error) {
	var mainDoc hclDocument
	var bucketsDoc hclDocument
	var dnsDoc hclDocument
	var rolesDoc hclDocument
	var runtimeInputsDoc hclDocument
	var vmInputsDoc hclDocument
	var infraInputsDoc hclDocument
	var outputsDoc hclDocument
	mainDoc.header = []string{
		"Cluster providers, VM templates, and Nstance infrastructure, including bootstrap configuration.",
	}
	bucketsDoc.header = []string{
		"Object storage buckets for Netsy cluster state and the container registry.",
	}
	dnsDoc.header = []string{
		"DNS records for provider load balancers. Tunnel DNS is user-managed.",
	}
	rolesDoc.header = []string{
		"Cloud permissions for cluster storage and secrets, including ingress certificates where needed.",
	}
	outputsDoc.header = []string{
		"Cluster endpoints, DNS setup details, and infrastructure references used by the CLI.",
	}
	runtimeInputsDoc.header = []string{
		"Runtime configuration inputs. Changes reconfigure agent-managed VMs without replacement.",
		"Shared SSH, telemetry, and registry settings also provision nst VMs; changing them rolls those VMs.",
		"Static access keys apply only to agent-managed VMs; nst uses its VM role credentials.",
	}
	vmInputsDoc.header = []string{
		"VM infrastructure inputs. Changes reconcile VM counts or trigger rolling VM replacement.",
	}
	infraInputsDoc.header = []string{
		"Cluster infrastructure inputs. Changes may add, replace, or remove provider resources.",
	}

	addClusterTerraformRequirements(&mainDoc, provider.Kind)

	if provider.Kind == "aws" {
		awsProvider := block("provider", "aws")
		awsProvider.Body.Attr("region", expr("local.provider_region"))
		if provider.Profile != "" {
			awsProvider.Body.Attr("profile", str(provider.Profile))
		}
		if provider.Account != "" {
			awsProvider.Body.Attr("allowed_account_ids", list(expr("local.provider_account")))
		}
		if len(provider.Tags) > 0 {
			defaultTags := block("default_tags")
			defaultTags.Body.Attr("tags", stringMapValue(provider.Tags))
			awsProvider.Body.Block(defaultTags)
		}
		mainDoc.AddBlock(awsProvider)

		caller := block("data", "aws_caller_identity", "current")
		mainDoc.AddBlock(caller)

		region := block("data", "aws_region", "current")
		mainDoc.AddBlock(region)
		partition := block("data", "aws_partition", "current")
		mainDoc.AddBlock(partition)
	} else {
		googleProvider := block("provider", "google")
		googleProvider.Body.Attr("project", expr("local.provider_project"))
		googleProvider.Body.Attr("region", expr("local.provider_region"))
		mainDoc.AddBlock(googleProvider)
	}

	manifests := make(map[VMTarget]VMConfigManifest, len(opts.VMConfigManifests))
	for _, manifest := range opts.VMConfigManifests {
		manifests[VMTarget{Kind: manifest.Kind, Arch: manifest.Arch}] = manifest
	}
	tunnelImages := map[VMTarget]string{}
	for _, target := range RequiredVMTargets(cfg) {
		manifest, ok := manifests[target]
		if !ok || len(manifest.JSON) == 0 {
			return nil, fmt.Errorf("missing vmconfig manifest %s/%s", target.Kind, target.Arch)
		}
		if cfg.Cluster.Tunnel != nil && (target.Kind == "knc" || (target.Kind == "nst" && cfg.Cluster.Seed.Name == seeds.Recommended)) {
			image, err := tunnelImage(cfg.Cluster.Tunnel.Kind, manifest)
			if err != nil {
				return nil, err
			}
			tunnelImages[target] = image
		}
		zones := []string{""}
		if target.Kind == "nst" {
			zones = sortedKeys(provider.Zones)
		}
		for _, zone := range zones {
			userdata := block("data", "podplane_userdata", safeName(target.Kind, target.Arch, zone))
			userdata.Body.Attr("manifest_json", expr("file("+quote("${path.module}/podplane.cluster."+manifest.Filename)+")"))
			userdata.Body.Attr("deps_mirror_url", str(opts.DepsMirrorURL))
			userdata.Body.Attr("provider_kind", expr("local.provider_kind"))
			if provider.Kind == "aws" {
				userdata.Body.Attr("aws_account_id", expr("local.aws_account_id"))
			} else {
				userdata.Body.Attr("google_project_id", expr("local.provider_project"))
			}
			userdata.Body.Attr("immutable_ssh_authorized_keys", expr("var.immutable_ssh_authorized_keys"))
			if provider.Kind == "aws" {
				userdata.Body.Attr("enable_ssm", expr("var.enable_ssm"))
			}
			if target.Kind == "nst" {
				userdata.Body.Attr("server_env", serverEnvValue(cfg, zone))
			}
			mainDoc.AddBlock(userdata)
		}
	}

	registryHostnameDefault := str(cfg.ResolvedRegistryHostname())
	var oidcSigningAlgsDefault hclValue = expr("null")
	if len(cfg.Cluster.OIDC.SigningAlgs) > 0 {
		oidcSigningAlgsDefault = stringValueList(cfg.Cluster.OIDC.SigningAlgs)
	}
	// Keep equivalent variables in vmconfig mutable.env order.
	runtimeVars := []struct {
		name        string
		description string
		typeExpr    string
		defaultVal  hclValue
	}{
		{"ssh_authorized_keys", "SSH login keys; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_enabled", "Telemetry state; existing VMs are reconfigured when set.", "bool", expr("null")},
		{"telemetry_log_cloudinit", "Cloud-init log collection; existing VMs are reconfigured when set.", "bool", expr("null")},
		{"telemetry_log_services", "Telemetry services; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_otlp_endpoint", "OTLP endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_bucket", "Telemetry bucket; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_endpoint", "Telemetry S3 endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_assume_role", "Telemetry role; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_access_key_id", "Static telemetry access key ID; prefer telemetry_s3_assume_role when available. Existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_secret_access_key", "Static telemetry secret access key; prefer telemetry_s3_assume_role when available. Existing VMs are reconfigured when set.", "string", expr("null")},
		{"oidc_issuer_url", "OIDC issuer; existing VMs are reconfigured.", "string", str(cfg.Cluster.OIDC.IssuerURL)},
		{"oidc_ca_cert", "OIDC CA certificate; existing VMs are reconfigured when set.", "string", expr("null")},
		{"oidc_signing_algs", "OIDC signing algorithms accepted by kube-apiserver; existing VMs are reconfigured. vmconfig defaults to RS256 when unset.", "list(string)", oidcSigningAlgsDefault},
		{"kube_api_etcd_servers", "etcd endpoints; existing VMs are reconfigured when set.", "string", expr("null")},
		{"kubernetes_api_hostname", "Kubernetes API hostname; existing VMs are reconfigured.", "string", str(resolvedAPIHostname(cfg))},
		{"kubernetes_api_port", "External Kubernetes API port used by clients; kube-apiserver listens internally on 6443.", "number", num(resolvedAPIPort(cfg))},
		{"kubernetes_cluster_cidr", "Pod CIDRs for control-plane services. Existing VMs are reconfigured, but Podplane does not migrate existing Pods, node CIDR allocations, CNI state, routes, or other networking state.", "list(string)", stringValueList(cfg.Cluster.Kubernetes.ClusterCIDR)},
		{"kubernetes_node_cidr_mask_size_ipv4", "IPv4 Pod CIDR prefix for new node allocations. Existing Node.spec.podCIDRs and CNI state are not resized or migrated; old allocations consume corresponding blocks in the new allocation grid.", "number", expr("null")},
		{"kubernetes_node_cidr_mask_size_ipv6", "IPv6 Pod CIDR prefix for new node allocations. Existing Node.spec.podCIDRs and CNI state are not resized or migrated; old allocations consume corresponding blocks in the new allocation grid.", "number", expr("null")},
		{"kubernetes_service_cidr", "Default Service CIDRs for control-plane services. Existing VMs are reconfigured, but Podplane does not migrate existing Services or other networking state; additional ServiceCIDR resources are separate.", "list(string)", stringValueList(serviceNetwork.CIDRs)},
		{"kube_log_level", "Kubernetes log level; existing VMs are reconfigured when set.", "number", expr("null")},
		{"aws_s3_use_path_style", "S3 path style; existing VMs are reconfigured when set.", "string", expr("null")},
		{"netsy_endpoint", "Netsy endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"netsy_access_key_id", "Static Netsy access key ID; prefer assume-role credentials when available. Existing VMs are reconfigured when set.", "string", expr("null")},
		{"netsy_secret_access_key", "Static Netsy secret access key; prefer assume-role credentials when available. Existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_enabled", "Registry state; existing VMs are reconfigured when set.", "bool", expr("null")},
		{"registry_endpoint", "Registry endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_access_key_id", "Static registry access key ID; prefer assume-role credentials when available. Existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_secret_access_key", "Static registry secret access key; prefer assume-role credentials when available. Existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_hostname", "Registry hostname; existing VMs are reconfigured when set.", "string", registryHostnameDefault},
	}
	sensitiveRuntimeVars := map[string]bool{
		"telemetry_s3_access_key_id":     true,
		"telemetry_s3_secret_access_key": true,
		"netsy_access_key_id":            true,
		"netsy_secret_access_key":        true,
		"registry_access_key_id":         true,
		"registry_secret_access_key":     true,
	}
	numberMax := map[string]int{
		"kubernetes_node_cidr_mask_size_ipv4": 32,
		"kubernetes_node_cidr_mask_size_ipv6": 128,
	}
	for _, item := range runtimeVars {
		variable := block("variable", item.name)
		variable.Body.Attr("description", str(item.description))
		variable.Body.Attr("type", expr(item.typeExpr))
		variable.Body.Attr("default", item.defaultVal)
		if sensitiveRuntimeVars[item.name] {
			variable.Body.Attr("sensitive", boolean(true))
		}
		if max := numberMax[item.name]; max > 0 {
			validation := block("validation")
			validation.Body.Attr("condition", expr(fmt.Sprintf("var.%s == null ? true : (var.%s >= 0 && var.%s <= %d && floor(var.%s) == var.%s)", item.name, item.name, item.name, max, item.name, item.name)))
			validation.Body.Attr("error_message", str(fmt.Sprintf("%s must be a whole number between 0 and %d.", item.name, max)))
			variable.Body.Block(validation)
		}
		runtimeInputsDoc.AddBlock(variable)
	}
	runtimeLocals := block("locals")
	runtimeLocals.Body.Attr("oidc_client_id", str(cfg.ResolvedClientID()))
	runtimeLocals.Body.Attr("oidc_username_claim", str(cfg.ResolvedUsernameClaim()))
	runtimeLocals.Body.Attr("oidc_groups_claim", str(cfg.ResolvedGroupsClaim()))
	runtimeInputsDoc.AddBlock(runtimeLocals)
	poolInstanceTypes := []hclObjectField{}
	poolSizes := []hclObjectField{}
	for _, name := range sortedKeys(cfg.Cluster.Pools) {
		pool := cfg.Cluster.Pools[name]
		poolInstanceTypes = append(poolInstanceTypes, field(name, str(pool.InstanceType)))
		poolSizes = append(poolSizes, field(name, num(pool.Size)))
	}
	if nat := cfg.Cluster.NAT; nat != nil && nat.Mode == "nstance" {
		for _, name := range sortedKeys(nat.Groups) {
			poolInstanceTypes = append(poolInstanceTypes, field(name, str(nat.Groups[name].InstanceType)))
		}
	}
	addInputVariable(&vmInputsDoc, "immutable_ssh_authorized_keys", "string", str(""), "Early-boot SSH keys; changing them rolls affected VMs.")
	addInputVariable(&vmInputsDoc, "pool_instance_types", "map(string)", object(poolInstanceTypes...), "Instance types by pool; changing one rolls that pool.")
	addInputVariable(&vmInputsDoc, "pool_sizes", "map(number)", object(poolSizes...), "Desired VM counts by pool; changes are reconciled by Nstance.")
	addInputVariable(&infraInputsDoc, "vpc_id", "string", str(provider.VPC.ID), "Existing VPC ID; changing it replaces network placement.")
	addInputVariable(&infraInputsDoc, "vpc_cidr_ipv4", "string", str(provider.VPC.V4CIDR), "Managed VPC IPv4 CIDR; changing it may replace networking.")
	addInputVariable(&infraInputsDoc, "enable_ipv6", "bool", boolean(provider.VPC.V6CIDR == "auto"), "IPv6 state; changing it updates cloud networking.")
	switch provider.Kind {
	case "aws":
		addInputVariable(&infraInputsDoc, "enable_ssm", "bool", boolean(true), "SSM access; changing it updates AWS infrastructure and VM bootstrap.")
	case "google":
		addInputVariable(&infraInputsDoc, "enable_iap", "bool", boolean(true), "IAP access to SSH and the Nstance operator API; changing it updates firewall rules.")
		addInputVariable(&vmInputsDoc, "enable_os_login", "bool", boolean(true), "IAM-based SSH on Nstance server VMs; changing it rolls those VMs.")
	}
	natMode := "provider"
	fixedAddresses := 0
	if nat := cfg.Cluster.NAT; nat != nil && nat.Mode == "nstance" {
		natMode = "nstance"
		fixedAddresses = nat.FixedPublicIPv4Count
	}
	addInputVariable(&infraInputsDoc, "nat_mode", "string", str(natMode), "Choose provider-managed NAT or Nstance-managed NAT. Changing modes requires destroying and recreating the cluster.")
	addInputVariable(&infraInputsDoc, "fixed_public_ipv4_count", "number", num(fixedAddresses), "Number of static public IPv4 addresses to reserve for each NAT subnet when using Nstance-managed NAT.")
	topology := block("locals")
	topology.Body.Attr("subnets", subnetsValue(provider))
	topology.Body.Attr("load_balancers", loadBalancersValue(provider))
	infraInputsDoc.AddBlock(topology)
	identity := block("locals")
	identity.Comments = []string{
		"Cluster identity and placement. Changing these values is not supported in place;",
		"destroy the cluster, then update podplane.cluster.jsonc and recreate it.",
	}
	identity.Body.Attr("cluster_id", str(cfg.Cluster.ID))
	identity.Body.Attr("spiffe_trust_domain", str(cfg.Cluster.SPIFFE.TrustDomain))
	identity.Body.Attr("name_prefix", str(cfg.Cluster.ID))
	identity.Body.Attr("provider_kind", str(provider.Kind))
	switch provider.Kind {
	case "aws":
		identity.Body.Attr("provider_account", str(provider.Account))
	case "google":
		identity.Body.Attr("provider_project", str(provider.Project))
	}
	identity.Body.Attr("provider_region", str(provider.Region))
	infraInputsDoc.AddBlock(identity)

	// Persist the first applied modes in Terraform state, including when users
	// apply the generated files directly or override nat_mode on the command line.
	exposureMode := "nlb"
	if cfg.Cluster.Tunnel != nil {
		exposureMode = "tunnel"
	}
	guard := block("resource", "terraform_data", "deployment_modes")
	guard.Body.Attr("input", object(
		identField("nat", expr("var.nat_mode")),
		identField("exposure", str(exposureMode)),
	))
	lifecycle := block("lifecycle")
	lifecycle.Body.Attr("ignore_changes", list(expr("input")))
	for _, mode := range []struct{ name, requested, message string }{
		{"nat", "var.nat_mode", "Changing NAT mode (provider <-> nstance) in place is not supported. Destroy the cluster, then recreate it with the desired mode."},
		{"exposure", quote(exposureMode), "Changing exposure mode (NLB <-> tunnel) in place is not supported. Destroy the cluster, then recreate it with the desired mode."},
	} {
		condition := block("postcondition")
		condition.Body.Attr("condition", expr("self.input."+mode.name+" == "+mode.requested))
		condition.Body.Attr("error_message", str(mode.message))
		lifecycle.Body.Block(condition)
	}
	guard.Body.Block(lifecycle)
	mainDoc.AddBlock(guard)

	locals := block("locals")
	if provider.Kind == "aws" {
		locals.Body.Attr("aws_account_id", expr("data.aws_caller_identity.current.account_id"))
		locals.Body.Attr("aws_region", expr("data.aws_region.current.region"))
		locals.Body.Attr("netsy_bucket_name", str("${local.cluster_id}-${local.aws_account_id}-netsy"))
		locals.Body.Attr("registry_bucket_name", str("${local.cluster_id}-${local.aws_account_id}-registry"))
	} else {
		locals.Body.Attr("netsy_bucket_name", str("${local.cluster_id}-${local.provider_project}-netsy"))
		locals.Body.Attr("registry_bucket_name", str("${local.cluster_id}-${local.provider_project}-registry"))
	}
	locals.Body.Attr("host_env", hostEnvValue(provider.Kind))
	locals.Body.Attr("mutable_env", mutableEnvValue(serviceNetwork, provider.Kind))
	apiServiceIPs := make([]string, len(serviceNetwork.API))
	for i, ip := range serviceNetwork.API {
		apiServiceIPs[i] = ip.String()
	}
	locals.Body.Attr("certificates", nstanceCertificatesValue(cfg, apiServiceIPs))
	locals.Body.Attr("templates", templatesValue(cfg, tunnelImages))
	mainDoc.AddBlock(locals)

	cluster := block("module", "cluster")
	addNstanceModuleSource(&cluster, opts, "cluster")
	cluster.Body.Attr("depends_on", list(expr("terraform_data.deployment_modes")))
	cluster.Body.Attr("cluster_id", expr("local.cluster_id"))
	cluster.Body.Attr("name_prefix", expr("local.name_prefix"))
	cluster.Body.Attr("shards", stringValueList(sortedKeys(provider.Zones)))
	secretPrefix := "/" + cfg.Cluster.ID + "/"
	if provider.Kind == "google" {
		secretPrefix = cfg.Cluster.ID + "-"
	}
	cluster.Body.Attr("secrets_prefix", str(secretPrefix))
	if provider.Profile != "" {
		cluster.Body.Attr("aws_profile", str(provider.Profile))
	}
	if len(provider.Tags) > 0 {
		cluster.Body.Attr("tags", stringMapValue(provider.Tags))
	}
	mainDoc.AddBlock(cluster)

	accountName := safeName("account", provider.Account, provider.Region)
	networkName := safeName("network", provider.Account, provider.Region)
	clusterID := block("output", "cluster_id")
	clusterID.Body.Attr("value", expr("local.cluster_id"))
	outputsDoc.AddBlock(clusterID)

	apiURL := block("output", "kubernetes_api_url")
	apiURL.Body.Attr("value", str("https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"))
	outputsDoc.AddBlock(apiURL)

	domainTargets := block("output", "domain_targets")
	domainTargets.Body.Attr("description", str("Configured domain names and their external routing targets."))
	domainTargets.Body.Attr("value", domainTargetsValue(cfg, networkName))
	outputsDoc.AddBlock(domainTargets)

	manualDNS := block("output", "manual_dns_records")
	manualDNS.Body.Attr("description", str("DNS records requiring manual setup. Tunnel DNS and HTTPS edge settings are user-managed."))
	manualDNS.Body.Attr("value", manualDNSRecordsValue(cfg, networkName))
	outputsDoc.AddBlock(manualDNS)

	account := block("module", accountName)
	addNstanceModuleSource(&account, opts, "account")
	account.Body.Attr("cluster", expr("module.cluster"))
	if provider.Kind == "aws" {
		account.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	}
	mainDoc.AddBlock(account)

	network := block("module", networkName)
	addNstanceModuleSource(&network, opts, "network")
	network.Body.Attr("depends_on", list(expr("terraform_data.deployment_modes")))
	network.Body.Attr("cluster", expr("module.cluster"))
	if provider.Kind == "aws" {
		network.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	}
	network.Body.Attr("vpc_id", expr("var.vpc_id"))
	network.Body.Attr("vpc_cidr_ipv4", expr("var.vpc_cidr_ipv4"))
	network.Body.Attr("ipv6_enabled", expr("var.enable_ipv6"))
	network.Body.Attr("nat_mode", expr("var.nat_mode"))
	network.Body.Attr("fixed_public_ipv4_count", expr("var.fixed_public_ipv4_count"))
	if provider.Kind == "google" {
		network.Body.Attr("project_id", expr("local.provider_project"))
		network.Body.Attr("region", expr("local.provider_region"))
	}
	network.Body.Attr("subnets", expr("local.subnets"))
	if len(provider.LoadBalancers) > 0 {
		network.Body.Attr("load_balancers", expr("local.load_balancers"))
	}
	mainDoc.AddBlock(network)
	if cfg.Cluster.NAT != nil && cfg.Cluster.NAT.Mode == "nstance" {
		addNATSubnetLookups(&mainDoc, provider)
	}
	if cfg.Cluster.Tunnel == nil {
		if provider.Kind == "aws" {
			addAWSDNS(&dnsDoc, cfg, networkName)
		} else {
			addGoogleDNS(&dnsDoc, cfg, networkName)
		}
	}

	for _, zone := range sortedKeys(provider.Zones) {
		moduleName := safeName("shard", zone)
		shard := block("module", moduleName)
		addNstanceModuleSource(&shard, opts, "shard")
		shard.Body.Attr("cluster", expr("module.cluster"))
		shard.Body.Attr("account", expr("module."+accountName))
		shard.Body.Attr("network", expr("module."+networkName))
		shard.Body.Attr("shard", str(zone))
		shard.Body.Attr("zone", str(zone))
		switch provider.Kind {
		case "aws":
			shard.Body.Attr("enable_ssm", expr("var.enable_ssm"))
		case "google":
			shard.Body.Attr("enable_iap", expr("var.enable_iap"))
			shard.Body.Attr("enable_os_login", expr("var.enable_os_login"))
		}
		shard.Body.Attr("certificates", expr("local.certificates"))
		shard.Body.Attr("templates", expr("local.templates"))
		shard.Body.Attr("groups", groupsValue(cfg, provider))
		shard.Body.Attr("server_arch", str(serverArch(provider)))
		shard.Body.Attr("server_userdata", expr("data.podplane_userdata."+safeName("nst", serverArch(provider), zone)+".content"))
		if cfg.Cluster.NAT != nil && cfg.Cluster.NAT.Mode == "nstance" {
			shard.Body.Attr("nat", natValue(cfg.Cluster.NAT, zone, networkName))
		}
		if cfg.Cluster.Tunnel != nil && cfg.Cluster.Seed.Name == seeds.Recommended {
			shard.Body.Attr("tunnels", wakeTunnelsValue(cfg, tunnelImages[VMTarget{Kind: "nst", Arch: serverArch(provider)}]))
		}
		mainDoc.AddBlock(shard)
	}

	if provider.Kind == "aws" {
		addPodplaneAWSBuckets(&bucketsDoc)
		addPodplaneAWSRoles(&rolesDoc, cfg, accountName)
	} else {
		addPodplaneGoogleBuckets(&bucketsDoc)
		addPodplaneGooglePermissions(&rolesDoc, cfg, accountName)
	}
	managedWorkloadCAKey := addWorkloadCAKey(&mainDoc, cfg, provider)

	if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
		storage := "s3"
		if provider.Kind == "google" {
			storage = "gcs"
		}
		seed := block("resource", "podplane_netsy_seed_"+storage, "cluster")
		seed.Body.Attr("cluster_config_path", str("${path.module}/"+filepath.Base(configPath)))
		if provider.Kind == "aws" {
			seed.Body.Attr("bucket", expr("aws_s3_bucket.podplane_cluster[\"netsy\"].bucket"))
			seed.Body.Attr("region", expr("local.aws_region"))
			if provider.Profile != "" {
				seed.Body.Attr("profile", str(provider.Profile))
			}
		} else {
			seed.Body.Attr("bucket", expr("google_storage_bucket.podplane_cluster[\"netsy\"].name"))
			seed.Body.Attr("project", expr("local.provider_project"))
		}
		seed.Body.Attr("values_content", seedValues(cfg, provider))
		if cfg.Cluster.Seed.Name == seeds.Recommended {
			addNstanceBootstrap(&mainDoc, &seed, provider, secretPrefix)
		}
		if managedWorkloadCAKey {
			dependencies := []hclValue{expr("podplane_workload_ca_key.cluster")}
			if defaultProvider := cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider]; defaultProvider.Kind == "aws" && provider.Kind == "aws" {
				dependencies = append(dependencies, expr("aws_iam_role_policy.podplane_workload_ca_key"))
			} else if defaultProvider.Kind == "gcp" && provider.Kind == "google" {
				dependencies = append(dependencies, expr("google_secret_manager_secret_iam_member.podplane_workload_ca_key"))
			}
			seed.Body.Attr("depends_on", list(dependencies...))
		}
		mainDoc.AddBlock(seed)
		if cfg.Cluster.Tunnel != nil && cfg.Cluster.Seed.Name == seeds.Recommended {
			addTunnelCAFiles(&mainDoc, provider)
		}
	}

	bucket := block("output", "nstance_bucket")
	bucket.Body.Attr("value", expr("module.cluster.bucket"))
	outputsDoc.AddBlock(bucket)

	shards := block("output", "nstance_shards")
	shards.Body.Attr("value", nstanceShardsValue(provider))
	outputsDoc.AddBlock(shards)

	if provider.Kind == "aws" {
		registryReadOnlyRole := block("output", "registry_read_only_role_arn")
		registryReadOnlyRole.Body.Attr("value", expr("aws_iam_role.podplane_cluster[\"registry-read-only\"].arn"))
		outputsDoc.AddBlock(registryReadOnlyRole)

		registryReadWriteRole := block("output", "registry_read_write_role_arn")
		registryReadWriteRole.Body.Attr("value", expr("aws_iam_role.podplane_cluster[\"registry-read-write\"].arn"))
		outputsDoc.AddBlock(registryReadWriteRole)

		netsyRole := block("output", "netsy_role_arn")
		netsyRole.Body.Attr("value", expr("aws_iam_role.podplane_cluster[\"netsy\"].arn"))
		outputsDoc.AddBlock(netsyRole)
		if route53ACMEEnabled(cfg) {
			acmeRole := block("output", "acme_route53_role_arn")
			acmeRole.Body.Attr("value", expr("aws_iam_role.acme_route53[0].arn"))
			outputsDoc.AddBlock(acmeRole)
		}
	}
	files := []File{
		{Name: "podplane.cluster.main.tf", Content: mainDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.buckets.tf", Content: bucketsDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.dns.tf", Content: dnsDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.roles.tf", Content: rolesDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.inputs.runtime.tf", Content: runtimeInputsDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.inputs.vm.tf", Content: vmInputsDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.inputs.infra.tf", Content: infraInputsDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.outputs.tf", Content: outputsDoc.String(), Type: FileTypeTerraform},
	}
	for _, target := range RequiredVMTargets(cfg) {
		manifest := manifests[target]
		files = append(files, File{
			Name:    "podplane.cluster." + manifest.Filename,
			Content: string(manifest.JSON),
			Type:    FileTypeJSON,
		})
	}
	return files, nil
}

// addWorkloadCAKey adds the metadata-only provider resource for a
// Podplane-managed backend. It reports whether it added the resource.
func addWorkloadCAKey(doc *hclDocument, cfg *clusterconfig.ClusterConfig, infrastructure clusterconfig.Provider) bool {
	provider := cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider]
	if provider.Kind == "vault" || provider.Kind == "openbao" {
		return false
	}
	prefix := provider.KeyPrefix
	if prefix == "" {
		prefix = cfg.Cluster.ID
	}
	resource := block("resource", "podplane_workload_ca_key", "cluster")
	resource.Body.Attr("key_prefix", str(prefix))
	switch {
	case provider.Kind == "aws" && provider.ObjectType == "secretsmanager":
		resource.Body.Attr("secrets_provider", str("aws-secrets-manager"))
	case provider.Kind == "aws" && provider.ObjectType == "ssmparameter":
		resource.Body.Attr("secrets_provider", str("aws-parameter-store"))
	case provider.Kind == "gcp":
		resource.Body.Attr("secrets_provider", str("google-secret-manager"))
		resource.Body.Attr("project", str(provider.ProjectID))
	}
	if provider.Kind == "aws" {
		region := provider.Region
		if region == "" {
			region = infrastructure.Region
		}
		resource.Body.Attr("region", str(region))
		if infrastructure.Profile != "" {
			resource.Body.Attr("profile", str(infrastructure.Profile))
		}
	}
	resource.Body.Attr("depends_on", list(expr("module.cluster")))
	doc.AddBlock(resource)
	return true
}

// managedAPIDomain returns the default domain index when its DNS provider can manage
// the configured Kubernetes API hostname.
func managedAPIDomain(cfg *clusterconfig.ClusterConfig) (int, bool) {
	if cfg.Cluster.Kubernetes.APILoadBalancer == "" || len(cfg.Cluster.Domains) == 0 {
		return 0, false
	}
	domain := cfg.Cluster.Domains[0]
	if domain.Provider == nil || !domainCoversHostname(domain.Zone, cfg.Cluster.Kubernetes.APIHostname) {
		return 0, false
	}
	return 0, true
}

// domainCoversHostname reports whether a DNS zone is authoritative for a
// hostname.
func domainCoversHostname(zone, hostname string) bool {
	return hostname == zone || strings.HasSuffix(hostname, "."+zone)
}

// apiRecordCoveredByDomain reports whether an existing domain apex record
// already targets the Kubernetes API load balancer.
func apiRecordCoveredByDomain(cfg *clusterconfig.ClusterConfig) bool {
	for _, domain := range cfg.Cluster.Domains {
		if domain.Zone == cfg.Cluster.Kubernetes.APIHostname && domain.ResolvedDomainLoadBalancer() == cfg.Cluster.Kubernetes.APILoadBalancer {
			return true
		}
	}
	return false
}

// domainTargetsValue builds concise outputs for every configured domain.
func domainTargetsValue(cfg *clusterconfig.ClusterConfig, networkName string) hclObject {
	fields := make([]hclObjectField, 0, len(cfg.Cluster.Domains))
	for _, domain := range cfg.Cluster.Domains {
		fields = append(fields, field(domain.Zone, object(
			identField("names", stringValueList([]string{domain.Zone, "*." + domain.Zone})),
			identField("target", endpointTarget(cfg, networkName, domain.ResolvedDomainLoadBalancer())),
			identField("managed", boolean(domain.Provider != nil && cfg.Cluster.Tunnel == nil)),
		)))
	}
	return object(fields...)
}

// manualDNSRecordsValue builds record-to-target outputs for DNS records that
// Podplane cannot manage.
func manualDNSRecordsValue(cfg *clusterconfig.ClusterConfig, networkName string) hclObject {
	fields := []hclObjectField{}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider != nil && cfg.Cluster.Tunnel == nil {
			continue
		}
		target := endpointTarget(cfg, networkName, domain.ResolvedDomainLoadBalancer())
		fields = append(fields, field(domain.Zone, target), field("*."+domain.Zone, target))
	}
	if _, managed := managedAPIDomain(cfg); cfg.Cluster.Tunnel != nil || (cfg.Cluster.Kubernetes.APILoadBalancer != "" && !managed && !apiRecordCoveredByDomain(cfg)) {
		target := endpointTarget(cfg, networkName, cfg.Cluster.Kubernetes.APILoadBalancer)
		fields = append(fields, field(cfg.Cluster.Kubernetes.APIHostname, target))
	}
	return object(fields...)
}

// hostEnvValue supplies shared non-secret settings for managed nodes and NST VMs.
func hostEnvValue(providerKind string) hclExpression {
	registry := `aws_s3_bucket.podplane_cluster["registry"].bucket`
	if providerKind == "google" {
		registry = `google_storage_bucket.podplane_cluster["registry"].name`
	}
	return expr(`{ for key, value in {
  SSH_AUTHORIZED_KEYS = var.ssh_authorized_keys
  TELEMETRY_ENABLED = var.telemetry_enabled == null ? null : tostring(var.telemetry_enabled)
  TELEMETRY_LOG_CLOUDINIT = var.telemetry_log_cloudinit == null ? null : tostring(var.telemetry_log_cloudinit)
  TELEMETRY_LOG_SERVICES = var.telemetry_log_services
  TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
  TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
  TELEMETRY_S3_REGION = local.provider_region
  TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
  TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
  AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
  REGISTRY_ENABLED = var.registry_enabled == null ? null : tostring(var.registry_enabled)
  REGISTRY_BUCKET = ` + registry + `
  REGISTRY_REGION = local.provider_region
  REGISTRY_ENDPOINT = var.registry_endpoint
  REGISTRY_HOSTNAME = var.registry_hostname
} : key => value if value != null }`)
}

// mutableEnvValue adds managed-node settings to the shared host configuration.
func mutableEnvValue(network clusterconfig.ServiceNetwork, providerKind string) hclExpression {
	dns := make([]string, len(network.CoreDNS))
	for i := range network.CoreDNS {
		dns[i] = network.CoreDNS[i].String()
	}
	storage := `NETSY_BUCKET = aws_s3_bucket.podplane_cluster["netsy"].bucket
  NETSY_ASSUME_ROLE = aws_iam_role.podplane_cluster["netsy"].arn
  REGISTRY_ASSUME_ROLE = aws_iam_role.podplane_cluster["registry-read-only"].arn`
	if providerKind == "google" {
		storage = `NETSY_BUCKET = google_storage_bucket.podplane_cluster["netsy"].name`
	}
	return expr(`merge(local.host_env, { for key, value in {
  ` + storage + `
  NETSY_REGION = local.provider_region
  OIDC_ISSUER = var.oidc_issuer_url
  OIDC_USERNAME_CLAIM = local.oidc_username_claim
  OIDC_SIGNING_ALGS = var.oidc_signing_algs == null ? null : join(",", var.oidc_signing_algs)
  KUBE_API_PUBLIC_HOSTNAME = var.kubernetes_api_hostname
  KUBE_SERVICE_ACCOUNT_ISSUER = "https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"
  KUBE_CLUSTER_CIDR = join(",", var.kubernetes_cluster_cidr)
  KUBE_SERVICE_CLUSTER_IP_RANGE = join(",", var.kubernetes_service_cidr)
  KUBE_CLUSTER_DNS = ` + quote(strings.Join(dns, ",")) + `
  TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
  TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
  OIDC_CA_CERT = var.oidc_ca_cert
  KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
  KUBE_LOG_LEVEL = var.kube_log_level == null ? null : tostring(var.kube_log_level)
  KUBE_NODE_CIDR_MASK_SIZE_IPV4 = var.kubernetes_node_cidr_mask_size_ipv4 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv4)
  KUBE_NODE_CIDR_MASK_SIZE_IPV6 = var.kubernetes_node_cidr_mask_size_ipv6 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv6)
  NETSY_ENDPOINT = var.netsy_endpoint
  NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
  NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
  REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
  REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
} : key => value if value != null })`)
}

// subnetsValue converts provider zones into the network module subnets object.
func subnetsValue(provider clusterconfig.Provider) hclObject {
	// entry associates a subnet with its zone and automatic IPv6 allocation index.
	type entry struct {
		zone      string
		subnet    clusterconfig.Subnet
		v6CIDRNum int
	}
	roles := map[string][]entry{}
	natRoles := map[string]string{}
	v6CIDRNum := 0
	for _, zone := range sortedKeys(provider.Zones) {
		for _, subnet := range provider.Zones[zone] {
			role := subnet.ResolvedRole()
			if subnet.Public && slices.Contains(subnet.Services, "nat") {
				natRoles[zone] = role
			}
			roles[role] = append(roles[role], entry{zone: zone, subnet: subnet, v6CIDRNum: v6CIDRNum})
			if subnet.V6CIDR == "auto" {
				v6CIDRNum++
			}
		}
	}
	roleFields := []hclObjectField{}
	for _, role := range sortedKeys(roles) {
		byZone := map[string][]entry{}
		for _, e := range roles[role] {
			byZone[e.zone] = append(byZone[e.zone], e)
		}
		zoneFields := []hclObjectField{}
		for _, zone := range sortedKeys(byZone) {
			subnets := make(hclList, 0, len(byZone[zone]))
			for _, entry := range byZone[zone] {
				subnets = append(subnets, subnetValue(entry.subnet, entry.v6CIDRNum, natRoles[zone]))
			}
			zoneFields = append(zoneFields, field(zone, subnets))
		}
		roleFields = append(roleFields, field(role, object(zoneFields...)))
	}
	return object(roleFields...)
}

// subnetValue converts one subnet config into the network module subnet object.
func subnetValue(subnet clusterconfig.Subnet, v6CIDRNum int, natRole string) hclInlineObject {
	fields := []hclObjectField{}
	if subnet.ID != "" {
		fields = append(fields, identField("existing", str(subnet.ID)))
	} else {
		fields = append(fields, identField("ipv4_cidr", str(subnet.V4CIDR)))
	}
	if subnet.V6CIDR == "auto" {
		fields = append(fields, identField("ipv6_netnum", num(v6CIDRNum)))
	} else if subnet.V6CIDR != "" {
		fields = append(fields, identField("ipv6_cidr", str(subnet.V6CIDR)))
	}
	if subnet.Public {
		fields = append(fields, identField("public", boolean(true)))
	}
	if slices.Contains(subnet.Services, "nat") {
		fields = append(fields, identField("nat_gateway", boolean(true)))
	}
	if !subnet.Public && natRole != "" {
		fields = append(fields, identField("nat_subnet", str(natRole)))
	}
	return inlineObject(fields...)
}

// loadBalancersValue converts named load balancers into the Nstance network
// module input.
func loadBalancersValue(provider clusterconfig.Provider) hclObject {
	if len(provider.LoadBalancers) == 0 {
		return nil
	}
	fields := []hclObjectField{}
	for _, name := range sortedKeys(provider.LoadBalancers) {
		loadBalancer := provider.LoadBalancers[name]
		listeners := append([]clusterconfig.Listener(nil), loadBalancer.Listeners...)
		slices.SortFunc(listeners, func(a, b clusterconfig.Listener) int { return a.Port - b.Port })
		listenerValues := make(hclList, 0, len(listeners))
		for _, listener := range listeners {
			listenerValues = append(listenerValues, inlineObject(
				identField("port", num(listener.Port)),
				identField("target_port", num(listener.ResolvedTargetPort())),
				identField("proxy_port", num(listener.ResolvedProxyPort())),
			))
		}
		lbFields := []hclObjectField{
			identField("listeners", listenerValues),
			identField("subnets", str(loadBalancer.Subnets)),
			identField("public", boolean(loadBalancer.Public)),
		}
		if provider.Kind == "google" {
			lbFields = append(lbFields,
				identField("backend_subnets", str(listeners[0].Pool)),
				identField("proxy_subnets", str("nstance")),
			)
		}
		fields = append(fields, field(name, inlineObject(lbFields...)))
	}
	return object(fields...)
}

// groupsValue converts cluster pools into the shard groups object expected by
// the Nstance Terraform module.
func groupsValue(cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider) hclObject {
	lbsByPool := make(map[string][]string)
	for name, loadBalancer := range provider.LoadBalancers {
		pool := loadBalancer.Listeners[0].Pool
		lbsByPool[pool] = append(lbsByPool[pool], name)
	}
	if cfg.Cluster.Tunnel != nil && cfg.Cluster.Seed.Name == seeds.Recommended {
		lbsByPool["control-plane"] = []string{"cluster"}
	}
	instanceType := "instance_type"
	if provider.Kind == "google" {
		instanceType = "machine_type"
	}
	poolFields := []hclObjectField{}
	for _, poolName := range sortedKeys(cfg.Cluster.Pools) {
		fields := []hclObjectField{
			identField("template", str(poolName)),
			identField("size", expr("var.pool_sizes["+quote(poolName)+"]")),
			identField("subnet_pool", str(poolName)),
			identField(instanceType, expr("var.pool_instance_types["+quote(poolName)+"]")),
			identField("vars", expr("merge(local.mutable_env, { NSTANCE_EBPF_PORTS = "+quote(activityPorts(cfg, poolName))+" })")),
		}
		if len(lbsByPool[poolName]) > 0 {
			slices.Sort(lbsByPool[poolName])
			fields = append(fields, identField("load_balancers", stringValueList(lbsByPool[poolName])))
		}
		poolFields = append(poolFields, field(poolName, object(fields...)))
	}
	if nat := cfg.Cluster.NAT; nat != nil && nat.Mode == "nstance" {
		for _, name := range sortedKeys(nat.Groups) {
			poolFields = append(poolFields, field(name, object(
				identField("template", str(name)),
				identField("subnet_pool", str(nat.Groups[name].SubnetPool)),
				identField(instanceType, expr("var.pool_instance_types["+quote(name)+"]")),
				identField("vars", object(identField("NAT_IPV4_SOURCE_CIDRS", natSourceCIDRs(provider)))),
			)))
		}
	}
	return object(field("default", object(poolFields...)))
}

// addInputVariable appends a generated Terraform variable declaration.
func addInputVariable(doc *hclDocument, name, typeExpr string, defaultVal hclValue, description string) {
	variable := block("variable", name)
	variable.Body.Attr("description", str(description))
	variable.Body.Attr("type", expr(typeExpr))
	variable.Body.Attr("default", defaultVal)
	doc.AddBlock(variable)
}

// templatesValue converts cluster pools into Nstance instance templates.
func templatesValue(cfg *clusterconfig.ClusterConfig, tunnelImages map[VMTarget]string) hclObject {
	fields := make([]hclObjectField, 0, len(cfg.Cluster.Pools))
	for _, poolName := range sortedKeys(cfg.Cluster.Pools) {
		pool := cfg.Cluster.Pools[poolName]
		kind := poolKind(poolName)
		target := VMTarget{Kind: kind, Arch: pool.Arch}
		userdataRef := "data.podplane_userdata." + target.dataName() + ".content"
		fields = append(fields, field(poolName, object(
			identField("kind", str(kind)),
			identField("arch", str(pool.Arch)),
			identField("vars", expr("local.mutable_env")),
			identField("userdata", inlineObject(
				identField("source", str("inline")),
				identField("encoding", str("base64")),
				identField("content", expr("base64encode("+userdataRef+")")),
			)),
			identField("files", nstanceTemplateFilesValue(kind, cfg, tunnelImages[target])),
			identField("args", imageArgs(cfg.Cluster.Providers[0].Kind, pool.Arch, pool.DiskSize)),
		)))
	}
	if nat := cfg.Cluster.NAT; nat != nil && nat.Mode == "nstance" {
		for _, name := range sortedKeys(nat.Groups) {
			arch := nat.Groups[name].Arch
			target := VMTarget{Kind: "nat", Arch: arch}
			fields = append(fields, field(name, object(
				identField("kind", str("nat")),
				identField("arch", str(arch)),
				identField("userdata", inlineObject(
					identField("source", str("inline")),
					identField("encoding", str("base64")),
					identField("content", expr("base64encode(data.podplane_userdata."+target.dataName()+".content)")),
				)),
				identField("files", inlineObject(field("mutable.env", inlineObject(
					identField("kind", str("env")),
					identField("template", object(identField("NAT_IPV4_SOURCE_CIDRS", natSourceCIDRs(cfg.Cluster.Providers[0])))),
				)))),
				identField("args", imageArgs(cfg.Cluster.Providers[0].Kind, arch, 0)),
			)))
		}
	}
	return object(fields...)
}

// RequiredVMTargets returns the unique, ordered VM targets needed by a cluster.
func RequiredVMTargets(cfg *clusterconfig.ClusterConfig) []VMTarget {
	set := make(map[VMTarget]struct{}, len(cfg.Cluster.Pools))
	for poolName, pool := range cfg.Cluster.Pools {
		set[VMTarget{Kind: poolKind(poolName), Arch: pool.Arch}] = struct{}{}
	}
	set[VMTarget{Kind: "nst", Arch: serverArch(cfg.Cluster.Providers[0])}] = struct{}{}
	if nat := cfg.Cluster.NAT; nat != nil && nat.Mode == "nstance" {
		for _, group := range nat.Groups {
			set[VMTarget{Kind: "nat", Arch: group.Arch}] = struct{}{}
		}
	}
	targets := make([]VMTarget, 0, len(set))
	for target := range set {
		targets = append(targets, target)
	}
	slices.SortFunc(targets, func(a, b VMTarget) int {
		if n := cmp.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		return cmp.Compare(a.Arch, b.Arch)
	})
	return targets
}

// nstanceCertificatesValue returns the certificate templates required by the
// vmconfig runtime files generated for Nstance-managed Podplane VMs.
func nstanceCertificatesValue(cfg *clusterconfig.ClusterConfig, apiServiceIPs []string) hclObject {
	var ingressDomains []string
	if cfg.Cluster.Tunnel != nil {
		for _, domain := range cfg.Cluster.Domains {
			ingressDomains = append(ingressDomains, domain.Zone)
		}
	}
	fields := []hclObjectField{}
	for _, cert := range clusterspec.Certificates("{{ .Cluster.ID }}", "", apiServiceIPs, ingressDomains) {
		dns := stringValueList(cert.DNS)
		if cert.Name == "kube-apiserver.server" {
			dns = append(dns, expr("var.kubernetes_api_hostname"))
		}
		certFields := []hclObjectField{
			identField("kind", str(cert.Kind)),
			identField("cn", str(cert.CN)),
			identField("dns", dns),
			identField("ip", stringValueList(cert.IP)),
			identField("ttl", num(cert.TTL)),
		}
		if len(cert.Organization) > 0 {
			certFields = append(certFields, identField("organization", stringValueList(cert.Organization)))
		}
		if len(cert.URI) > 0 {
			certFields = append(certFields, identField("uri", stringValueList(cert.URI)))
		}
		fields = append(fields, field(cert.Name, inlineObject(certFields...)))
	}
	return object(fields...)
}

// nstanceTemplateFilesValue returns the runtime files Nstance should generate
// for one vmconfig kind, including certificate files backed by agent keys.
func nstanceTemplateFilesValue(kind string, cfg *clusterconfig.ClusterConfig, tunnelImage string) hclInlineObject {
	ingress := cfg.Cluster.Tunnel != nil && len(cfg.Cluster.Domains) > 0
	fields := []hclObjectField{
		field("mutable.env", inlineObject(
			identField("kind", str("env")),
			identField("template", expr("merge(local.mutable_env, { NSTANCE_EBPF_PORTS = \"{{ .Vars.NSTANCE_EBPF_PORTS }}\" })")),
		)),
	}
	for _, name := range clusterspec.CertificateFiles(kind, ingress) {
		fields = append(fields, field(name+".crt", inlineObject(
			identField("kind", str("certificate")),
			identField("template", str(name)),
			identField("key", inlineObject(
				identField("source", str("agent")),
				identField("name", str(name)),
			)),
		)))
	}
	if cfg.Cluster.Tunnel != nil && kind == "knc" {
		fields = append(fields, nodeTunnelFiles(cfg, tunnelImage)...)
	}
	return inlineObject(fields...)
}

// poolKind returns the Nstance three-letter instance kind for a Podplane pool.
func poolKind(poolName string) string {
	if poolName == "control-plane" {
		return "knc"
	}
	return "knd"
}

// nstanceShardsValue builds the Terraform output value for shard cleanup
// metadata.
func nstanceShardsValue(provider clusterconfig.Provider) hclObject {
	fields := []hclObjectField{}
	for _, zone := range sortedKeys(provider.Zones) {
		moduleName := safeName("shard", zone)
		fields = append(fields, field(zone, object(
			identField("config_key", expr("module."+moduleName+".config_key")),
			identField("server_ips", expr("module."+moduleName+".server_ips")),
		)))
	}
	return object(fields...)
}

// stringMapValue converts a string map into an ordered HCL object.
func stringMapValue(values map[string]string) hclObject {
	fields := []hclObjectField{}
	for _, key := range sortedKeys(values) {
		fields = append(fields, field(key, str(values[key])))
	}
	return object(fields...)
}

// resolvedAPIHostname returns the required Kubernetes API hostname.
func resolvedAPIHostname(cfg *clusterconfig.ClusterConfig) string {
	return cfg.Cluster.Kubernetes.APIHostname
}

// resolvedAPIPort returns the configured Kubernetes API port or the default.
func resolvedAPIPort(cfg *clusterconfig.ClusterConfig) int {
	return cfg.ResolvedAPIPort()
}
