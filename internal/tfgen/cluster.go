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
	return renderAWSCluster(configPath, cfg, provider, opts, network), nil
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
		identField("version", str(">= 1.0.0")),
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

	var registryHostnameDefault hclValue = expr("null")
	if cfg.Cluster.Registry.Hostname != "" {
		registryHostnameDefault = str(cfg.Cluster.Registry.Hostname)
	}
	var oidcSigningAlgsDefault hclValue = expr("null")
	if len(cfg.Cluster.OIDC.SigningAlgs) > 0 {
		oidcSigningAlgsDefault = stringValueList(cfg.Cluster.OIDC.SigningAlgs)
	}
	runtimeVars := []struct {
		name        string
		description string
		typeExpr    string
		defaultVal  hclValue
	}{
		{"oidc_issuer_url", "OIDC issuer; existing VMs are reconfigured.", "string", str(cfg.Cluster.OIDC.IssuerURL)},
		{"oidc_signing_algs", "OIDC signing algorithms accepted by kube-apiserver; existing VMs are reconfigured. vmconfig defaults to RS256 when unset.", "list(string)", oidcSigningAlgsDefault},
		{"kubernetes_api_hostname", "Kubernetes API hostname; existing VMs are reconfigured.", "string", str(resolvedAPIHostname(cfg))},
		{"kubernetes_api_port", "External Kubernetes API port used by clients; kube-apiserver listens internally on 6443.", "number", num(resolvedAPIPort(cfg))},
		{"kubernetes_cluster_cidr", "Pod CIDRs for control-plane services. Existing VMs are reconfigured, but Podplane does not migrate existing Pods, node CIDR allocations, CNI state, routes, or other networking state.", "list(string)", stringValueList(cfg.Cluster.Kubernetes.ClusterCIDR)},
		{"kubernetes_node_cidr_mask_size_ipv4", "IPv4 Pod CIDR prefix for new node allocations. Existing Node.spec.podCIDRs and CNI state are not resized or migrated; old allocations consume corresponding blocks in the new allocation grid.", "number", expr("null")},
		{"kubernetes_node_cidr_mask_size_ipv6", "IPv6 Pod CIDR prefix for new node allocations. Existing Node.spec.podCIDRs and CNI state are not resized or migrated; old allocations consume corresponding blocks in the new allocation grid.", "number", expr("null")},
		{"kubernetes_service_cidr", "Default Service CIDRs for control-plane services. Existing VMs are reconfigured, but Podplane does not migrate existing Services or other networking state; additional ServiceCIDR resources are separate.", "list(string)", stringValueList(serviceNetwork.CIDRs)},
		{"registry_hostname", "Registry hostname; existing VMs are reconfigured when set.", "string", registryHostnameDefault},
		{"ssh_authorized_keys", "SSH login keys; existing VMs are reconfigured when set.", "string", expr("null")},
		{"kube_api_etcd_servers", "etcd endpoints; existing VMs are reconfigured when set.", "string", expr("null")},
		{"oidc_ca_cert", "OIDC CA certificate; existing VMs are reconfigured when set.", "string", expr("null")},
		{"kube_log_level", "Kubernetes log level; existing VMs are reconfigured when set.", "number", expr("null")},
		{"netsy_endpoint", "Netsy endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"netsy_access_key_id", "Netsy access key; existing VMs are reconfigured when set.", "string", expr("null")},
		{"netsy_secret_access_key", "Netsy secret key; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_enabled", "Telemetry state; existing VMs are reconfigured when set.", "bool", expr("null")},
		{"telemetry_log_services", "Telemetry services; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_log_cloudinit", "Cloud-init log collection; existing VMs are reconfigured when set.", "bool", expr("null")},
		{"telemetry_s3_bucket", "Telemetry bucket; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_endpoint", "Telemetry S3 endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_assume_role", "Telemetry role; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_access_key_id", "Telemetry access key; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_s3_secret_access_key", "Telemetry secret key; existing VMs are reconfigured when set.", "string", expr("null")},
		{"telemetry_otlp_endpoint", "OTLP endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_enabled", "Registry state; existing VMs are reconfigured when set.", "bool", expr("null")},
		{"registry_endpoint", "Registry endpoint; existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_access_key_id", "Registry access key; existing VMs are reconfigured when set.", "string", expr("null")},
		{"registry_secret_access_key", "Registry secret key; existing VMs are reconfigured when set.", "string", expr("null")},
		{"aws_s3_use_path_style", "S3 path style; existing VMs are reconfigured when set.", "string", expr("null")},
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
	cluster.Body.Attr("cluster_id", expr("local.cluster_id"))
	cluster.Body.Attr("name_prefix", expr("local.name_prefix"))
	if provider.Profile != "" {
		cluster.Body.Attr("aws_profile", str(provider.Profile))
	}
	if len(provider.Tags) > 0 {
		cluster.Body.Attr("tags", stringMapValue(provider.Tags))
	}
	mainDoc.AddBlock(cluster)

	clusterID := block("output", "cluster_id")
	clusterID.Body.Attr("value", expr("local.cluster_id"))
	outputsDoc.AddBlock(clusterID)

	apiURL := block("output", "kubernetes_api_url")
	apiURL.Body.Attr("value", str("https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"))
	outputsDoc.AddBlock(apiURL)

	accountName := safeName("account", provider.Account, provider.Region)
	networkName := safeName("network", provider.Account, provider.Region)
	account := block("module", accountName)
	account.Body.Attr("source", str("nstance-dev/nstance/aws//modules/account"))
	account.Body.Attr("cluster", expr("module.cluster"))
	account.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	mainDoc.AddBlock(account)

	network := block("module", networkName)
	network.Body.Attr("source", str("nstance-dev/nstance/aws//modules/network"))
	network.Body.Attr("cluster", expr("module.cluster"))
	network.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	network.Body.Attr("vpc_id", expr("var.vpc_id"))
	network.Body.Attr("vpc_cidr_ipv4", expr("var.vpc_cidr_ipv4"))
	network.Body.Attr("enable_ipv6", expr("var.enable_ipv6"))
	network.Body.Attr("subnets", expr("local.subnets"))
	if len(provider.LoadBalancer.Listeners) > 0 {
		network.Body.Attr("load_balancers", expr("local.load_balancers"))
	}
	mainDoc.AddBlock(network)

	for _, zone := range sortedKeys(provider.Zones) {
		moduleName := safeName("shard", zone)
		shard := block("module", moduleName)
		shard.Body.Attr("source", str("nstance-dev/nstance/aws//modules/shard"))
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
	addPodplaneAWSRoles(&rolesDoc, accountName)

	if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
		seed := block("resource", "podplane_netsy_seed_s3", "cluster")
		seed.Body.Attr("cluster_config_path", str("${path.module}/"+filepath.Base(configPath)))
		seed.Body.Attr("bucket", expr("aws_s3_bucket.podplane_cluster[\"netsy\"].bucket"))
		seed.Body.Attr("region", expr("local.aws_region"))
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

	mutableEnv := block("output", "mutable_env")
	mutableEnv.Body.Attr("value", expr("local.mutable_env"))
	outputsDoc.AddBlock(mutableEnv)

	registryReadOnlyRole := block("output", "registry_read_only_role_arn")
	registryReadOnlyRole.Body.Attr("value", expr("aws_iam_role.podplane_cluster[\"registry-read-only\"].arn"))
	outputsDoc.AddBlock(registryReadOnlyRole)

	registryReadWriteRole := block("output", "registry_read_write_role_arn")
	registryReadWriteRole.Body.Attr("value", expr("aws_iam_role.podplane_cluster[\"registry-read-write\"].arn"))
	outputsDoc.AddBlock(registryReadWriteRole)

	netsyRole := block("output", "netsy_role_arn")
	netsyRole.Body.Attr("value", expr("aws_iam_role.podplane_cluster[\"netsy\"].arn"))
	outputsDoc.AddBlock(netsyRole)
	files := []File{
		{Name: "podplane.cluster.main.tf", Content: mainDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.cluster.buckets.tf", Content: bucketsDoc.String(), Type: FileTypeTerraform},
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
func addPodplaneAWSRoles(doc *hclDocument, accountName string) {
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

	addPodplaneKNCPolicy(doc, accountName)
}

// addPodplaneKNCPolicy allows Podplane worker nodes to assume only the
// Podplane-generated workload roles they need.
func addPodplaneKNCPolicy(doc *hclDocument, accountName string) {
	policy := block("data", "aws_iam_policy_document", "podplane_knc")

	assumeWorkloadRoles := block("statement")
	assumeWorkloadRoles.Body.Attr("sid", str("AssumePodplaneWorkloadRoles"))
	assumeWorkloadRoles.Body.Attr("actions", stringValueList([]string{"sts:AssumeRole"}))
	assumeWorkloadRoles.Body.Attr("resources", list(
		expr("aws_iam_role.podplane_cluster[\"netsy\"].arn"),
		expr("aws_iam_role.podplane_cluster[\"registry-read-only\"].arn"),
		expr("aws_iam_role.podplane_cluster[\"registry-read-write\"].arn"),
	))
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
		zone   string
		subnet clusterconfig.Subnet
	}
	roles := map[string][]entry{}
	for _, zone := range sortedKeys(provider.Zones) {
		for _, subnet := range provider.Zones[zone] {
			role := subnetRole(subnet)
			roles[role] = append(roles[role], entry{zone: zone, subnet: subnet})
		}
	}
	roleFields := []hclObjectField{}
	for _, role := range sortedKeys(roles) {
		byZone := map[string][]clusterconfig.Subnet{}
		for _, e := range roles[role] {
			byZone[e.zone] = append(byZone[e.zone], e.subnet)
		}
		zoneFields := []hclObjectField{}
		for _, zone := range sortedKeys(byZone) {
			subnets := make(hclList, 0, len(byZone[zone]))
			for i, subnet := range byZone[zone] {
				subnets = append(subnets, subnetValue(subnet, i))
			}
			zoneFields = append(zoneFields, field(zone, subnets))
		}
		roleFields = append(roleFields, field(role, object(zoneFields...)))
	}
	return object(roleFields...)
}

// subnetValue converts one subnet config into the network module subnet object.
func subnetValue(subnet clusterconfig.Subnet, index int) hclInlineObject {
	fields := []hclObjectField{}
	if subnet.ID != "" {
		fields = append(fields, identField("existing", str(subnet.ID)))
	} else {
		fields = append(fields, identField("ipv4_cidr", str(subnet.V4CIDR)))
	}
	if subnet.V6CIDR == "auto" {
		fields = append(fields, identField("ipv6_netnum", num(index)))
	}
	if subnet.Public {
		fields = append(fields, identField("public", boolean(true)))
	}
	if slices.Contains(subnet.Services, "nat") {
		fields = append(fields, identField("nat_gateway", boolean(true)))
	}
	if !subnet.Public && subnetRole(subnet) != "public" {
		fields = append(fields, identField("nat_subnet", str("public")))
	}
	return inlineObject(fields...)
}

// subnetRole returns the generated subnet role for the network module.
func subnetRole(subnet clusterconfig.Subnet) string {
	if subnet.Pool != "" {
		return subnet.Pool
	}
	if slices.Contains(subnet.Services, "nstance") {
		return "nstance"
	}
	if subnet.Public && (slices.Contains(subnet.Services, "nat") || slices.Contains(subnet.Services, "nlb")) {
		return "public"
	}
	return "services"
}

// loadBalancersValue converts load balancer listeners into an ordered HCL
// object for the network module.
func loadBalancersValue(provider clusterconfig.Provider) hclObject {
	if len(provider.LoadBalancer.Listeners) == 0 {
		return nil
	}
	portsByPool := map[string][]int{}
	for _, listener := range provider.LoadBalancer.Listeners {
		portsByPool[listener.Pool] = append(portsByPool[listener.Pool], listener.Port)
	}
	fields := []hclObjectField{}
	for _, pool := range sortedKeys(portsByPool) {
		ports := portsByPool[pool]
		name := "internal-" + pool
		subnets := pool
		if provider.LoadBalancer.Public {
			name = "public-" + pool
			subnets = "public"
		}
		portValues := make(hclList, 0, len(ports))
		for _, port := range ports {
			portValues = append(portValues, num(port))
		}
		fields = append(fields, field(name, inlineObject(
			identField("ports", portValues),
			identField("subnets", str(subnets)),
			identField("public", boolean(provider.LoadBalancer.Public)),
		)))
	}
	return object(fields...)
}

// groupsValue converts cluster pools into the shard groups object expected by
// the Nstance Terraform module.
func groupsValue(cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider) hclObject {
	lbsByPool := map[string][]string{}
	for _, listener := range provider.LoadBalancer.Listeners {
		name := "internal-" + listener.Pool
		if provider.LoadBalancer.Public {
			name = "public-" + listener.Pool
		}
		if !slices.Contains(lbsByPool[listener.Pool], name) {
			lbsByPool[listener.Pool] = append(lbsByPool[listener.Pool], name)
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
			fields = append(fields, identField("load_balancers", stringValueList(lbsByPool[poolName])))
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

// resolvedAPIHostname returns the configured Kubernetes API hostname or a
// generated fallback.
func resolvedAPIHostname(cfg *clusterconfig.ClusterConfig) string {
	if cfg.Cluster.Kubernetes.APIHostname != "" {
		return cfg.Cluster.Kubernetes.APIHostname
	}
	if len(cfg.Cluster.Domains) > 0 && cfg.Cluster.Domains[0].Zone != "" {
		return "k8s." + cfg.Cluster.Domains[0].Zone
	}
	return cfg.Cluster.ID + ".k8s.local"
}

// resolvedAPIPort returns the configured Kubernetes API port or the default.
func resolvedAPIPort(cfg *clusterconfig.ClusterConfig) int {
	if cfg.Cluster.Kubernetes.APIPort != 0 {
		return cfg.Cluster.Kubernetes.APIPort
	}
	return 6443
}
