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

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterspec"
	"github.com/podplane/podplane/pkg/seeds"
)

// ClusterOptions provides dependency inputs needed to render cluster Terraform.
type ClusterOptions struct {
	DepsMirrorURL     string
	VMConfigManifests []VMConfigManifest
}

// VMConfigManifest is one pinned vmconfig manifest used by generated Terraform.
type VMConfigManifest struct {
	Kind     string
	Arch     string
	Filename string
	JSON     []byte
}

type vmTarget struct {
	kind string
	arch string
}

// dataName returns this target's Terraform-safe data source name.
func (t vmTarget) dataName() string {
	return safeName(t.kind, t.arch)
}

// GenerateCluster renders managed Terraform files for a cluster config path.
func GenerateCluster(configPath string, cfg *clusterconfig.ClusterConfig, opts ClusterOptions) ([]File, error) {
	network, err := clusterconfig.ServiceNetworkFromCIDRs(cfg.Cluster.Kubernetes.ServiceCIDR)
	if err != nil {
		return nil, err
	}
	if err := clusterconfig.Validate(cfg); err != nil {
		return nil, err
	}
	provider := cfg.Cluster.Providers[0]
	if provider.Kind != "aws" {
		return nil, fmt.Errorf("cluster provider %q is not supported", provider.Kind)
	}
	if err := validateDNSProviders(cfg); err != nil {
		return nil, err
	}
	return renderAWSCluster(configPath, cfg, provider, opts, network), nil
}

// validateDNSProviders rejects DNS providers that managed Terraform generation
// does not implement.
func validateDNSProviders(cfg *clusterconfig.ClusterConfig) error {
	for i, domain := range cfg.Cluster.Domains {
		if domain.Provider != nil && domain.Provider.Kind != "aws-route53" {
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

// renderAWSCluster renders the AWS cluster Terraform files.
func renderAWSCluster(configPath string, cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider, opts ClusterOptions, serviceNetwork clusterconfig.ServiceNetwork) []File {
	var mainDoc hclDocument
	var bucketsDoc hclDocument
	var dnsDoc hclDocument
	var rolesDoc hclDocument
	var runtimeInputsDoc hclDocument
	var vmInputsDoc hclDocument
	var infraInputsDoc hclDocument
	var outputsDoc hclDocument
	runtimeInputsDoc.header = []string{
		"Runtime configuration inputs. Changes are pushed to existing VMs and applied without replacement,",
		"which means if you only change and apply one of these, you should only see the Nstance server",
		"configuration file in the planned changes.",
	}
	vmInputsDoc.header = []string{
		"VM infrastructure inputs. Changes reconcile VM counts or trigger rolling VM replacement.",
	}
	infraInputsDoc.header = []string{
		"Cluster infrastructure inputs. Changes may add, replace, or remove provider resources.",
	}

	terraform := block("terraform")
	terraform.Body.Attr("required_version", str(">= 1.6.0"))
	requiredProviders := block("required_providers")
	requiredProviders.Body.Attr("aws", object(
		identField("source", str("hashicorp/aws")),
		identField("version", str(">= 6.0")),
	))
	requiredProviders.Body.Attr("podplane", object(
		identField("source", str("podplane/podplane")),
		identField("version", str(">= 1.2.0")),
	))
	terraform.Body.Block(requiredProviders)
	mainDoc.AddBlock(terraform)

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

	manifests := make(map[vmTarget]VMConfigManifest, len(opts.VMConfigManifests))
	for _, manifest := range opts.VMConfigManifests {
		manifests[vmTarget{kind: manifest.Kind, arch: manifest.Arch}] = manifest
	}
	for _, target := range requiredVMTargets(cfg) {
		manifest, ok := manifests[target]
		if !ok || len(manifest.JSON) == 0 {
			panic(fmt.Errorf("missing vmconfig manifest %s/%s", target.kind, target.arch))
		}
		userdata := block("data", "podplane_userdata", target.dataName())
		userdata.Body.Attr("manifest_json", expr("file("+quote("${path.module}/podplane.cluster."+manifest.Filename)+")"))
		userdata.Body.Attr("deps_mirror_url", str(opts.DepsMirrorURL))
		userdata.Body.Attr("provider_kind", expr("local.provider_kind"))
		userdata.Body.Attr("aws_account_id", expr("local.aws_account_id"))
		userdata.Body.Attr("immutable_ssh_authorized_keys", expr("var.immutable_ssh_authorized_keys"))
		userdata.Body.Attr("enable_ssm", expr("var.enable_ssm"))
		mainDoc.AddBlock(userdata)
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
	addInputVariable(&vmInputsDoc, "immutable_ssh_authorized_keys", "string", str(""), "Early-boot SSH keys; changing them rolls affected VMs.")
	addInputVariable(&vmInputsDoc, "pool_instance_types", "map(string)", object(poolInstanceTypes...), "Instance types by pool; changing one rolls that pool.")
	addInputVariable(&vmInputsDoc, "pool_sizes", "map(number)", object(poolSizes...), "Desired VM counts by pool; changes are reconciled by Nstance.")
	addInputVariable(&infraInputsDoc, "vpc_id", "string", str(provider.VPC.ID), "Existing VPC ID; changing it replaces network placement.")
	addInputVariable(&infraInputsDoc, "vpc_cidr_ipv4", "string", str(provider.VPC.V4CIDR), "Managed VPC IPv4 CIDR; changing it may replace networking.")
	addInputVariable(&infraInputsDoc, "enable_ipv6", "bool", boolean(provider.VPC.V6CIDR == "auto"), "IPv6 state; changing it updates cloud networking.")
	addInputVariable(&infraInputsDoc, "enable_ssm", "bool", boolean(true), "SSM access; changing it updates cloud infrastructure and VM bootstrap.")
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
	identity.Body.Attr("name_prefix", str(cfg.Cluster.ID))
	identity.Body.Attr("provider_kind", str(provider.Kind))
	identity.Body.Attr("provider_account", str(provider.Account))
	identity.Body.Attr("provider_region", str(provider.Region))
	infraInputsDoc.AddBlock(identity)

	locals := block("locals")
	locals.Body.Attr("aws_account_id", expr("data.aws_caller_identity.current.account_id"))
	locals.Body.Attr("aws_region", expr("data.aws_region.current.region"))
	locals.Body.Attr("netsy_bucket_name", str("${local.cluster_id}-${local.aws_account_id}-netsy"))
	locals.Body.Attr("registry_bucket_name", str("${local.cluster_id}-${local.aws_account_id}-registry"))
	locals.Body.Attr("mutable_env", mutableEnvValue(serviceNetwork))
	apiServiceIPs := make([]string, len(serviceNetwork.API))
	for i, ip := range serviceNetwork.API {
		apiServiceIPs[i] = ip.String()
	}
	locals.Body.Attr("certificates", nstanceCertificatesValue(apiServiceIPs))
	locals.Body.Attr("templates", templatesValue(cfg))
	mainDoc.AddBlock(locals)

	cluster := block("module", "cluster")
	cluster.Body.Attr("source", str("nstance-dev/nstance/aws//modules/cluster"))
	cluster.Body.Attr("version", str("~> 2.0"))
	cluster.Body.Attr("cluster_id", expr("local.cluster_id"))
	cluster.Body.Attr("name_prefix", expr("local.name_prefix"))
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
	domainTargets.Body.Attr("description", str("Configured domain names and their load-balancer DNS targets."))
	domainTargets.Body.Attr("value", domainTargetsValue(cfg, networkName))
	outputsDoc.AddBlock(domainTargets)

	manualDNS := block("output", "manual_dns_records")
	manualDNS.Body.Attr("description", str("DNS records requiring manual setup. Use CNAME where valid, or an apex alias or flattening record."))
	manualDNS.Body.Attr("value", manualDNSRecordsValue(cfg, networkName))
	outputsDoc.AddBlock(manualDNS)

	account := block("module", accountName)
	account.Body.Attr("source", str("nstance-dev/nstance/aws//modules/account"))
	account.Body.Attr("version", str("~> 2.0"))
	account.Body.Attr("cluster", expr("module.cluster"))
	account.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	mainDoc.AddBlock(account)

	network := block("module", networkName)
	network.Body.Attr("source", str("nstance-dev/nstance/aws//modules/network"))
	network.Body.Attr("version", str("~> 2.0"))
	network.Body.Attr("cluster", expr("module.cluster"))
	network.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	network.Body.Attr("vpc_id", expr("var.vpc_id"))
	network.Body.Attr("vpc_cidr_ipv4", expr("var.vpc_cidr_ipv4"))
	network.Body.Attr("enable_ipv6", expr("var.enable_ipv6"))
	network.Body.Attr("subnets", expr("local.subnets"))
	if len(provider.LoadBalancers) > 0 {
		network.Body.Attr("load_balancers", expr("local.load_balancers"))
	}
	mainDoc.AddBlock(network)
	addAWSDNS(&dnsDoc, cfg, networkName)

	for _, zone := range sortedKeys(provider.Zones) {
		moduleName := safeName("shard", zone)
		shard := block("module", moduleName)
		shard.Body.Attr("source", str("nstance-dev/nstance/aws//modules/shard"))
		shard.Body.Attr("version", str("~> 2.0"))
		shard.Body.Attr("cluster", expr("module.cluster"))
		shard.Body.Attr("account", expr("module."+accountName))
		shard.Body.Attr("network", expr("module."+networkName))
		shard.Body.Attr("shard", str(zone))
		shard.Body.Attr("zone", str(zone))
		shard.Body.Attr("enable_ssm", expr("var.enable_ssm"))
		shard.Body.Attr("certificates", expr("local.certificates"))
		shard.Body.Attr("templates", expr("local.templates"))
		shard.Body.Attr("groups", groupsValue(cfg, provider))
		mainDoc.AddBlock(shard)
	}

	addPodplaneAWSBuckets(&bucketsDoc)
	addPodplaneAWSRoles(&rolesDoc, cfg, accountName)

	if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
		seed := block("resource", "podplane_netsy_seed_s3", "cluster")
		seed.Body.Attr("cluster_config_path", str("${path.module}/"+filepath.Base(configPath)))
		seed.Body.Attr("bucket", expr("aws_s3_bucket.podplane_cluster[\"netsy\"].bucket"))
		seed.Body.Attr("region", expr("local.aws_region"))
		if route53ACMEEnabled(cfg) {
			seed.Body.Attr("values_content", route53SeedValues())
		}
		if provider.Profile != "" {
			seed.Body.Attr("profile", str(provider.Profile))
		}
		mainDoc.AddBlock(seed)
	}

	bucket := block("output", "nstance_bucket")
	bucket.Body.Attr("value", expr("module.cluster.bucket"))
	outputsDoc.AddBlock(bucket)

	shards := block("output", "nstance_shards")
	shards.Body.Attr("value", nstanceShardsValue(provider))
	outputsDoc.AddBlock(shards)

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
		certManagerRole := block("output", "cert_manager_route53_role_arn")
		certManagerRole.Body.Attr("value", expr("aws_iam_role.cert_manager_route53[0].arn"))
		outputsDoc.AddBlock(certManagerRole)
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
	for _, target := range requiredVMTargets(cfg) {
		manifest := manifests[target]
		files = append(files, File{
			Name:    "podplane.cluster." + manifest.Filename,
			Content: string(manifest.JSON),
			Type:    FileTypeJSON,
		})
	}
	return files
}

// route53SeedValues returns the values overlay containing
// tf-resolved cert-manager identity and Route53 solver settings.
func route53SeedValues() hclValue {
	return expr(`yamlencode({
  platform = { components = {
    apps = { cert-manager = { namespaceAnnotations = {
      "iam.amazonaws.com/allowed-roles" = jsonencode([aws_iam_role.cert_manager_route53[0].arn])
    } } }
    values = {
      cert-manager = { cert-manager = { podAnnotations = {
        "iam.amazonaws.com/role" = aws_iam_role.cert_manager_route53[0].arn
      } } }
      platform-certs = { platform = { certs = { ingress = { acme = {
        solvers = [for name, zone in data.aws_route53_zone.managed : {
          dnsZones = [name]
          route53 = { hostedZoneID = zone.zone_id, region = local.aws_region }
        }]
      } } } } }
    }
  } }
})`)
}

// addAWSDNS adds Route53 aliases for managed domains and the managed
// Kubernetes API hostname.
func addAWSDNS(doc *hclDocument, cfg *clusterconfig.ClusterConfig, networkName string) {
	locals := block("locals")
	locals.Body.Attr("managed_dns_zones", managedDNSZonesValue(cfg))
	locals.Body.Attr("managed_dns_records", managedDNSRecordsValue(cfg))
	doc.AddBlock(locals)

	zone := block("data", "aws_route53_zone", "managed")
	zone.Body.Attr("for_each", expr("local.managed_dns_zones"))
	zone.Body.Attr("zone_id", expr("each.value.hosted_zone_id"))
	zone.Body.Attr("name", expr("each.value.hosted_zone_id == null ? each.key : null"))
	zone.Body.Attr("private_zone", expr("each.value.hosted_zone_id == null ? false : null"))
	doc.AddBlock(zone)

	addRoute53Aliases(doc, "managed_ipv4", "A", "local.managed_dns_records", networkName)
	addRoute53Aliases(doc, "managed_ipv6", "AAAA", "var.enable_ipv6 ? local.managed_dns_records : {}", networkName)
}

// addRoute53Aliases adds one iterated Route53 alias resource.
func addRoute53Aliases(doc *hclDocument, name, recordType, records, networkName string) {
	record := block("resource", "aws_route53_record", name)
	record.Body.Attr("for_each", expr(records))
	record.Body.Attr("zone_id", expr("data.aws_route53_zone.managed[each.value.zone].zone_id"))
	record.Body.Attr("name", expr("each.key"))
	record.Body.Attr("type", str(recordType))
	alias := block("alias")
	alias.Body.Attr("name", expr(fmt.Sprintf("module.%s.load_balancers[each.value.load_balancer].dns_name", networkName)))
	alias.Body.Attr("zone_id", expr(fmt.Sprintf("module.%s.load_balancers[each.value.load_balancer].zone_id", networkName)))
	alias.Body.Attr("evaluate_target_health", boolean(false))
	record.Body.Block(alias)
	doc.AddBlock(record)
}

// managedDNSZonesValue builds Route53 zone lookup configuration keyed by zone.
func managedDNSZonesValue(cfg *clusterconfig.ClusterConfig) hclObject {
	fields := []hclObjectField{}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider == nil {
			continue
		}
		var hostedZoneID hclValue = expr("null")
		if domain.Provider.HostedZoneID != "" {
			hostedZoneID = str(domain.Provider.HostedZoneID)
		}
		fields = append(fields, field(domain.Zone, object(
			identField("hosted_zone_id", hostedZoneID),
		)))
	}
	return object(fields...)
}

// managedDNSRecordsValue builds managed hostnames keyed by exact record name.
func managedDNSRecordsValue(cfg *clusterconfig.ClusterConfig) hclObject {
	fields := []hclObjectField{}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider == nil {
			continue
		}
		record := object(
			identField("zone", str(domain.Zone)),
			identField("load_balancer", str(domain.ResolvedDomainLoadBalancer())),
		)
		fields = append(fields, field(domain.Zone, record), field("*."+domain.Zone, record))
	}
	if domainIndex, managed := managedAPIDomain(cfg); managed && !apiRecordCoveredByDomain(cfg) {
		domain := cfg.Cluster.Domains[domainIndex]
		fields = append(fields, field(cfg.Cluster.Kubernetes.APIHostname, object(
			identField("zone", str(domain.Zone)),
			identField("load_balancer", str(cfg.Cluster.Kubernetes.APILoadBalancer)),
		)))
	}
	return object(fields...)
}

// managedAPIDomain returns the default domain index when Route53 can manage
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
		target := fmt.Sprintf("module.%s.load_balancers[%s].dns_name", networkName, quote(domain.ResolvedDomainLoadBalancer()))
		fields = append(fields, field(domain.Zone, object(
			identField("names", stringValueList([]string{domain.Zone, "*." + domain.Zone})),
			identField("target", expr(target)),
			identField("managed", boolean(domain.Provider != nil)),
		)))
	}
	return object(fields...)
}

// manualDNSRecordsValue builds record-to-target outputs for DNS records that
// Podplane cannot manage.
func manualDNSRecordsValue(cfg *clusterconfig.ClusterConfig, networkName string) hclObject {
	fields := []hclObjectField{}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider != nil {
			continue
		}
		target := expr(fmt.Sprintf("module.%s.load_balancers[%s].dns_name", networkName, quote(domain.ResolvedDomainLoadBalancer())))
		fields = append(fields, field(domain.Zone, target), field("*."+domain.Zone, target))
	}
	if _, managed := managedAPIDomain(cfg); cfg.Cluster.Kubernetes.APILoadBalancer != "" && !managed && !apiRecordCoveredByDomain(cfg) {
		target := expr(fmt.Sprintf("module.%s.load_balancers[%s].dns_name", networkName, quote(cfg.Cluster.Kubernetes.APILoadBalancer)))
		fields = append(fields, field(cfg.Cluster.Kubernetes.APIHostname, target))
	}
	return object(fields...)
}

// addPodplaneAWSBuckets adds Podplane-owned S3 buckets.
func addPodplaneAWSBuckets(doc *hclDocument) {
	bucketLocals := block("locals")
	bucketLocals.Body.Attr("buckets", object(
		field("netsy", expr("local.netsy_bucket_name")),
		field("registry", expr("local.registry_bucket_name")),
	))
	doc.AddBlock(bucketLocals)

	bucket := block("resource", "aws_s3_bucket", "podplane_cluster")
	bucket.Body.Attr("for_each", expr("local.buckets"))
	bucket.Body.Attr("bucket", expr("each.value"))
	doc.AddBlock(bucket)

	publicAccess := block("resource", "aws_s3_bucket_public_access_block", "podplane_cluster")
	publicAccess.Body.Attr("for_each", expr("local.buckets"))
	publicAccess.Body.Attr("bucket", expr("aws_s3_bucket.podplane_cluster[each.key].id"))
	publicAccess.Body.Attr("block_public_acls", boolean(true))
	publicAccess.Body.Attr("block_public_policy", boolean(true))
	publicAccess.Body.Attr("ignore_public_acls", boolean(true))
	publicAccess.Body.Attr("restrict_public_buckets", boolean(true))
	doc.AddBlock(publicAccess)

	encryption := block("resource", "aws_s3_bucket_server_side_encryption_configuration", "podplane_cluster")
	encryption.Body.Attr("for_each", expr("local.buckets"))
	encryption.Body.Attr("bucket", expr("aws_s3_bucket.podplane_cluster[each.key].id"))
	rule := block("rule")
	applyDefault := block("apply_server_side_encryption_by_default")
	applyDefault.Body.Attr("sse_algorithm", str("AES256"))
	rule.Body.Block(applyDefault)
	encryption.Body.Block(rule)
	doc.AddBlock(encryption)
}

// addPodplaneAWSRoles adds Podplane workload roles that are intentionally
// outside the Nstance account/network/shard modules.
func addPodplaneAWSRoles(doc *hclDocument, cfg *clusterconfig.ClusterConfig, accountName string) {
	roleLocals := block("locals")
	roleLocals.Body.Attr("roles", object(
		field("netsy", object(
			identField("bucket", str("netsy")),
			identField("bucket_actions", stringValueList([]string{"s3:ListBucket", "s3:ListBucketMultipartUploads"})),
			identField("object_actions", stringValueList([]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:GetObjectAttributes", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"})),
		)),
		field("registry-read-only", object(
			identField("bucket", str("registry")),
			identField("bucket_actions", stringValueList([]string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"})),
			identField("object_actions", stringValueList([]string{"s3:GetObject", "s3:ListMultipartUploadParts"})),
		)),
		field("registry-read-write", object(
			identField("bucket", str("registry")),
			identField("bucket_actions", stringValueList([]string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"})),
			identField("object_actions", stringValueList([]string{"s3:GetObject", "s3:ListMultipartUploadParts", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"})),
		)),
	))
	doc.AddBlock(roleLocals)

	assume := block("data", "aws_iam_policy_document", "assume_from_knc")
	statement := block("statement")
	statement.Body.Attr("actions", stringValueList([]string{"sts:AssumeRole"}))
	principals := block("principals")
	principals.Body.Attr("type", str("AWS"))
	principals.Body.Attr("identifiers", list(expr("module."+accountName+".agent_iam_role_arn")))
	statement.Body.Block(principals)
	assume.Body.Block(statement)
	doc.AddBlock(assume)

	role := block("resource", "aws_iam_role", "podplane_cluster")
	role.Body.Attr("for_each", expr("local.roles"))
	role.Body.Attr("name", str("${local.name_prefix}-${each.key}"))
	role.Body.Attr("assume_role_policy", expr("data.aws_iam_policy_document.assume_from_knc.json"))
	doc.AddBlock(role)

	policyDocument := block("data", "aws_iam_policy_document", "podplane_cluster")
	policyDocument.Body.Attr("for_each", expr("local.roles"))
	bucketStatement := block("statement")
	bucketStatement.Body.Attr("actions", expr("each.value.bucket_actions"))
	bucketStatement.Body.Attr("resources", list(expr("aws_s3_bucket.podplane_cluster[each.value.bucket].arn")))
	policyDocument.Body.Block(bucketStatement)
	objectStatement := block("statement")
	objectStatement.Body.Attr("actions", expr("each.value.object_actions"))
	objectStatement.Body.Attr("resources", list(expr("\"${aws_s3_bucket.podplane_cluster[each.value.bucket].arn}/*\"")))
	policyDocument.Body.Block(objectStatement)
	doc.AddBlock(policyDocument)

	policy := block("resource", "aws_iam_role_policy", "podplane_cluster")
	policy.Body.Attr("for_each", expr("local.roles"))
	policy.Body.Attr("name", str("${local.name_prefix}-${each.key}-policy"))
	policy.Body.Attr("role", expr("aws_iam_role.podplane_cluster[each.key].id"))
	policy.Body.Attr("policy", expr("data.aws_iam_policy_document.podplane_cluster[each.key].json"))
	doc.AddBlock(policy)

	if route53ACMEEnabled(cfg) {
		addCertManagerRoute53Role(doc)
	}
	addPodplaneKNCPolicy(doc, cfg, accountName)
}

// addCertManagerRoute53Role adds the least-privilege role assumed by the
// cert-manager controller through kube2iam.
func addCertManagerRoute53Role(doc *hclDocument) {
	role := block("resource", "aws_iam_role", "cert_manager_route53")
	role.Body.Attr("count", num(1))
	role.Body.Attr("name", str("${local.name_prefix}-cert-manager-route53"))
	role.Body.Attr("assume_role_policy", expr("data.aws_iam_policy_document.assume_from_knc.json"))
	doc.AddBlock(role)

	policyDocument := block("data", "aws_iam_policy_document", "cert_manager_route53")
	changeRecords := block("statement")
	changeRecords.Body.Attr("sid", str("ChangeDNS01Records"))
	changeRecords.Body.Attr("actions", stringValueList([]string{"route53:ChangeResourceRecordSets"}))
	changeRecords.Body.Attr("resources", expr("[for zone in data.aws_route53_zone.managed : zone.arn]"))
	recordTypes := block("condition")
	recordTypes.Body.Attr("test", str("ForAllValues:StringEquals"))
	recordTypes.Body.Attr("variable", str("route53:ChangeResourceRecordSetsRecordTypes"))
	recordTypes.Body.Attr("values", stringValueList([]string{"TXT"}))
	changeRecords.Body.Block(recordTypes)
	recordActions := block("condition")
	recordActions.Body.Attr("test", str("ForAllValues:StringEquals"))
	recordActions.Body.Attr("variable", str("route53:ChangeResourceRecordSetsActions"))
	recordActions.Body.Attr("values", stringValueList([]string{"UPSERT", "DELETE"}))
	changeRecords.Body.Block(recordActions)
	policyDocument.Body.Block(changeRecords)
	getChange := block("statement")
	getChange.Body.Attr("sid", str("ReadDNS01Changes"))
	getChange.Body.Attr("actions", stringValueList([]string{"route53:GetChange"}))
	getChange.Body.Attr("resources", stringValueList([]string{"arn:aws:route53:::change/*"}))
	policyDocument.Body.Block(getChange)
	doc.AddBlock(policyDocument)

	policy := block("resource", "aws_iam_role_policy", "cert_manager_route53")
	policy.Body.Attr("count", num(1))
	policy.Body.Attr("name", str("${local.name_prefix}-cert-manager-route53-policy"))
	policy.Body.Attr("role", expr("aws_iam_role.cert_manager_route53[0].id"))
	policy.Body.Attr("policy", expr("data.aws_iam_policy_document.cert_manager_route53.json"))
	doc.AddBlock(policy)
}

// addPodplaneKNCPolicy allows Podplane worker nodes to assume only the
// Podplane-generated workload roles they need.
func addPodplaneKNCPolicy(doc *hclDocument, cfg *clusterconfig.ClusterConfig, accountName string) {
	policy := block("data", "aws_iam_policy_document", "podplane_knc")

	assumeWorkloadRoles := block("statement")
	assumeWorkloadRoles.Body.Attr("sid", str("AssumePodplaneWorkloadRoles"))
	assumeWorkloadRoles.Body.Attr("actions", stringValueList([]string{"sts:AssumeRole"}))
	resources := []hclValue{
		expr("aws_iam_role.podplane_cluster[\"netsy\"].arn"),
		expr("aws_iam_role.podplane_cluster[\"registry-read-only\"].arn"),
		expr("aws_iam_role.podplane_cluster[\"registry-read-write\"].arn"),
	}
	if route53ACMEEnabled(cfg) {
		resources = append(resources, expr("aws_iam_role.cert_manager_route53[0].arn"))
	}
	assumeWorkloadRoles.Body.Attr("resources", list(resources...))
	policy.Body.Block(assumeWorkloadRoles)

	describeRegions := block("statement")
	describeRegions.Body.Attr("sid", str("DescribeRegions"))
	describeRegions.Body.Attr("actions", stringValueList([]string{"ec2:DescribeRegions"}))
	describeRegions.Body.Attr("resources", stringValueList([]string{"*"}))
	policy.Body.Block(describeRegions)
	doc.AddBlock(policy)

	rolePolicy := block("resource", "aws_iam_role_policy", "podplane_knc")
	rolePolicy.Body.Attr("name", str("${local.name_prefix}-podplane-knc-policy"))
	rolePolicy.Body.Attr("role", expr("module."+accountName+".agent_iam_role_name"))
	rolePolicy.Body.Attr("policy", expr("data.aws_iam_policy_document.podplane_knc.json"))
	doc.AddBlock(rolePolicy)
}

// route53ACMEEnabled reports whether generated infrastructure must provision
// the cert-manager Route53 identity.
func route53ACMEEnabled(cfg *clusterconfig.ClusterConfig) bool {
	if cfg.Cluster.ACME == nil {
		return false
	}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider != nil && domain.Provider.Kind == "aws-route53" {
			return true
		}
	}
	return false
}

// mutableEnvValue returns the Terraform expression for vmconfig mutable.env
// values generated from Terraform-managed resources and generated variables.
func mutableEnvValue(network clusterconfig.ServiceNetwork) hclExpression {
	dns := make([]string, len(network.CoreDNS))
	for i := range network.CoreDNS {
		dns[i] = network.CoreDNS[i].String()
	}
	return expr(`{ for key, value in {
  TELEMETRY_S3_REGION = local.aws_region
  OIDC_ISSUER = var.oidc_issuer_url
  OIDC_SIGNING_ALGS = var.oidc_signing_algs == null ? null : join(",", var.oidc_signing_algs)
  KUBE_API_PUBLIC_HOSTNAME = var.kubernetes_api_hostname
  KUBE_SERVICE_ACCOUNT_ISSUER = "https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"
  KUBE_CLUSTER_CIDR = join(",", var.kubernetes_cluster_cidr)
  KUBE_SERVICE_CLUSTER_IP_RANGE = join(",", var.kubernetes_service_cidr)
  KUBE_CLUSTER_DNS = ` + quote(strings.Join(dns, ",")) + `
  NETSY_BUCKET = aws_s3_bucket.podplane_cluster["netsy"].bucket
  NETSY_REGION = local.aws_region
  NETSY_ASSUME_ROLE = aws_iam_role.podplane_cluster["netsy"].arn
  REGISTRY_BUCKET = aws_s3_bucket.podplane_cluster["registry"].bucket
  REGISTRY_REGION = local.aws_region
  REGISTRY_ASSUME_ROLE = aws_iam_role.podplane_cluster["registry-read-only"].arn
  SSH_AUTHORIZED_KEYS = var.ssh_authorized_keys
  TELEMETRY_ENABLED = var.telemetry_enabled == null ? null : tostring(var.telemetry_enabled)
  TELEMETRY_LOG_CLOUDINIT = var.telemetry_log_cloudinit == null ? null : tostring(var.telemetry_log_cloudinit)
  TELEMETRY_LOG_SERVICES = var.telemetry_log_services
  TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
  TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
  TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
  TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
  TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
  TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
  OIDC_CA_CERT = var.oidc_ca_cert
  KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
  KUBE_LOG_LEVEL = var.kube_log_level == null ? null : tostring(var.kube_log_level)
  KUBE_NODE_CIDR_MASK_SIZE_IPV4 = var.kubernetes_node_cidr_mask_size_ipv4 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv4)
  KUBE_NODE_CIDR_MASK_SIZE_IPV6 = var.kubernetes_node_cidr_mask_size_ipv6 == null ? null : tostring(var.kubernetes_node_cidr_mask_size_ipv6)
  AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
  NETSY_ENDPOINT = var.netsy_endpoint
  NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
  NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
  REGISTRY_ENABLED = var.registry_enabled == null ? null : tostring(var.registry_enabled)
  REGISTRY_ENDPOINT = var.registry_endpoint
  REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
  REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
  REGISTRY_HOSTNAME = var.registry_hostname
} : key => value if value != null }`)
}

// subnetsValue converts provider zones into the network module subnets object.
func subnetsValue(provider clusterconfig.Provider) hclObject {
	type entry struct {
		zone      string
		subnet    clusterconfig.Subnet
		v6CIDRNum int
	}
	roles := map[string][]entry{}
	v6CIDRNum := 0
	for _, zone := range sortedKeys(provider.Zones) {
		for _, subnet := range provider.Zones[zone] {
			role := subnet.ResolvedRole()
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
				subnets = append(subnets, subnetValue(entry.subnet, entry.v6CIDRNum))
			}
			zoneFields = append(zoneFields, field(zone, subnets))
		}
		roleFields = append(roleFields, field(role, object(zoneFields...)))
	}
	return object(roleFields...)
}

// subnetValue converts one subnet config into the network module subnet object.
func subnetValue(subnet clusterconfig.Subnet, v6CIDRNum int) hclInlineObject {
	fields := []hclObjectField{}
	if subnet.ID != "" {
		fields = append(fields, identField("existing", str(subnet.ID)))
	} else {
		fields = append(fields, identField("ipv4_cidr", str(subnet.V4CIDR)))
	}
	if subnet.V6CIDR == "auto" {
		fields = append(fields, identField("ipv6_netnum", num(v6CIDRNum)))
	}
	if subnet.Public {
		fields = append(fields, identField("public", boolean(true)))
	}
	if slices.Contains(subnet.Services, "nat") {
		fields = append(fields, identField("nat_gateway", boolean(true)))
	}
	if !subnet.Public && subnet.ResolvedRole() != "public" {
		fields = append(fields, identField("nat_subnet", str("public")))
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
			targetPort := listener.TargetPort
			if targetPort == 0 {
				targetPort = listener.Port
			}
			listenerValues = append(listenerValues, inlineObject(
				identField("port", num(listener.Port)),
				identField("target_port", num(targetPort)),
			))
		}
		fields = append(fields, field(name, inlineObject(
			identField("listeners", listenerValues),
			identField("subnets", str(loadBalancer.Subnets)),
			identField("public", boolean(loadBalancer.Public)),
		)))
	}
	return object(fields...)
}

// groupsValue converts cluster pools into the shard groups object expected by
// the Nstance Terraform module.
func groupsValue(cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider) hclObject {
	lbsByPool := make(map[string]map[string][]int)
	for name, loadBalancer := range provider.LoadBalancers {
		for _, listener := range loadBalancer.Listeners {
			if lbsByPool[listener.Pool] == nil {
				lbsByPool[listener.Pool] = make(map[string][]int)
			}
			lbsByPool[listener.Pool][name] = append(lbsByPool[listener.Pool][name], listener.Port)
		}
	}
	for _, loadBalancers := range lbsByPool {
		for _, ports := range loadBalancers {
			slices.Sort(ports)
		}
	}
	poolFields := []hclObjectField{}
	for _, poolName := range sortedKeys(cfg.Cluster.Pools) {
		pool := cfg.Cluster.Pools[poolName]
		fields := []hclObjectField{
			identField("template", str(poolName)),
			identField("size", expr("var.pool_sizes["+quote(poolName)+"]")),
			identField("subnet_pool", str(poolName)),
			identField("instance_type", expr("var.pool_instance_types["+quote(poolName)+"]")),
			identField("vars", expr("local.mutable_env")),
		}
		if pool.DiskSize > 0 {
			fields = append(fields, identField("disk_size", num(pool.DiskSize)))
		}
		if len(lbsByPool[poolName]) > 0 {
			loadBalancerFields := make([]hclObjectField, 0, len(lbsByPool[poolName]))
			for _, name := range sortedKeys(lbsByPool[poolName]) {
				ports := make(hclList, 0, len(lbsByPool[poolName][name]))
				for _, port := range lbsByPool[poolName][name] {
					ports = append(ports, num(port))
				}
				loadBalancerFields = append(loadBalancerFields, field(name, ports))
			}
			fields = append(fields, identField("load_balancers", object(loadBalancerFields...)))
		}
		poolFields = append(poolFields, field(poolName, object(fields...)))
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
func templatesValue(cfg *clusterconfig.ClusterConfig) hclObject {
	fields := make([]hclObjectField, 0, len(cfg.Cluster.Pools))
	for _, poolName := range sortedKeys(cfg.Cluster.Pools) {
		pool := cfg.Cluster.Pools[poolName]
		kind := poolKind(poolName)
		target := vmTarget{kind: kind, arch: pool.Arch}
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
			identField("files", nstanceTemplateFilesValue(kind)),
			identField("args", inlineObject(
				identField("ImageId", str("{{ .Image.debian_13_"+pool.Arch+" }}")),
			)),
		)))
	}
	return object(fields...)
}

// requiredVMTargets returns the unique, ordered VM targets used by cluster pools.
func requiredVMTargets(cfg *clusterconfig.ClusterConfig) []vmTarget {
	set := make(map[vmTarget]struct{}, len(cfg.Cluster.Pools))
	for poolName, pool := range cfg.Cluster.Pools {
		set[vmTarget{kind: poolKind(poolName), arch: pool.Arch}] = struct{}{}
	}
	targets := make([]vmTarget, 0, len(set))
	for target := range set {
		targets = append(targets, target)
	}
	slices.SortFunc(targets, func(a, b vmTarget) int {
		if n := cmp.Compare(a.kind, b.kind); n != 0 {
			return n
		}
		return cmp.Compare(a.arch, b.arch)
	})
	return targets
}

// nstanceCertificatesValue returns the certificate templates required by the
// vmconfig runtime files generated for Nstance-managed Podplane VMs.
func nstanceCertificatesValue(apiServiceIPs []string) hclObject {
	fields := []hclObjectField{}
	for _, cert := range clusterspec.Certificates("{{ .Cluster.ID }}", "", apiServiceIPs) {
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
func nstanceTemplateFilesValue(kind string) hclInlineObject {
	fields := []hclObjectField{
		field("mutable.env", inlineObject(
			identField("kind", str("env")),
			identField("template", expr("local.mutable_env")),
		)),
	}
	for _, name := range clusterspec.CertificateFiles(kind) {
		fields = append(fields, field(name+".crt", inlineObject(
			identField("kind", str("certificate")),
			identField("template", str(name)),
			identField("key", inlineObject(
				identField("source", str("agent")),
				identField("name", str(name)),
			)),
		)))
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
	if cfg.Cluster.Kubernetes.APIPort != 0 {
		return cfg.Cluster.Kubernetes.APIPort
	}
	return 6443
}
