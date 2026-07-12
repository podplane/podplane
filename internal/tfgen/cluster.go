// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterspec"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/userdata"
	"github.com/podplane/podplane/pkg/seeds"
)

// ClusterOptions provides dependency inputs needed to render cluster Terraform.
type ClusterOptions struct {
	DepsMirrorURL     string
	VMConfigManifests map[string]*deps.Manifest
}

// GenerateCluster renders managed Terraform files for a cluster config path.
func GenerateCluster(configPath string, cfg *clusterconfig.ClusterConfig, opts ClusterOptions) ([]File, error) {
	if err := clusterconfig.Validate(cfg); err != nil {
		return nil, err
	}
	provider := cfg.Cluster.Providers[0]
	if provider.Kind != "aws" {
		return nil, fmt.Errorf("cluster provider %q is not supported", provider.Kind)
	}
	return renderAWSCluster(configPath, cfg, provider, opts), nil
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
func renderAWSCluster(configPath string, cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider, opts ClusterOptions) []File {
	var mainDoc hclDocument
	var variablesDoc hclDocument
	var outputsDoc hclDocument

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
	awsProvider.Body.Attr("region", str(provider.Region))
	if provider.Profile != "" {
		awsProvider.Body.Attr("profile", str(provider.Profile))
	}
	if provider.Account != "" {
		awsProvider.Body.Attr("allowed_account_ids", stringValueList([]string{provider.Account}))
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

	mutableVars := []struct {
		name        string
		description string
		typeExpr    string
		defaultVal  hclValue
	}{
		{"ssh_authorized_keys", "SSH public keys allowed for VM login.", "string", str("")},
		{"immutable_ssh_authorized_keys", "Immutable SSH public keys for debugging early VM boot failures. Changing this value rotates affected VMs.", "string", str("")},
		{"kube_api_etcd_servers", "etcd-compatible endpoint list used by kube-apiserver.", "string", str("")},
		{"oidc_ca_cert", "Base64-encoded OIDC issuer CA certificate.", "string", str("")},
		{"kube_log_level", "Kubernetes component log verbosity.", "number", num(2)},
		{"netsy_endpoint", "Custom Netsy object-storage endpoint URL.", "string", str("")},
		{"netsy_access_key_id", "Netsy object-storage access key ID for non-IAM providers.", "string", str("")},
		{"netsy_secret_access_key", "Netsy object-storage secret access key for non-IAM providers.", "string", str("")},
		{"telemetry_enabled", "Enable VM telemetry/log forwarding.", "bool", hclBool(false)},
		{"telemetry_log_services", "Comma-separated systemd services to include in telemetry logs.", "string", str("")},
		{"telemetry_log_cloudinit", "Include cloud-init logs in telemetry.", "bool", hclBool(true)},
		{"telemetry_s3_bucket", "Telemetry S3 bucket name.", "string", str("")},
		{"telemetry_s3_endpoint", "Custom telemetry S3 endpoint URL.", "string", str("")},
		{"telemetry_s3_assume_role", "Telemetry S3 IAM role ARN to assume.", "string", str("")},
		{"telemetry_s3_access_key_id", "Telemetry S3 access key ID for non-IAM providers.", "string", str("")},
		{"telemetry_s3_secret_access_key", "Telemetry S3 secret access key for non-IAM providers.", "string", str("")},
		{"telemetry_otlp_endpoint", "OTLP endpoint for telemetry export.", "string", str("")},
		{"registry_enabled", "Enable the VM-hosted registry service.", "bool", hclBool(true)},
		{"registry_hostname", "Hostname used by clients to reach the registry.", "string", str(cfg.Cluster.Registry.Hostname)},
		{"registry_endpoint", "Custom registry object-storage endpoint URL.", "string", str("")},
		{"registry_access_key_id", "Registry object-storage access key ID for non-IAM providers.", "string", str("")},
		{"registry_secret_access_key", "Registry object-storage secret access key for non-IAM providers.", "string", str("")},
		{"aws_s3_use_path_style", "Whether S3 clients should use path-style URLs.", "string", str("")},
		{"enable_ssm", "Enable AWS Systems Manager Session Manager access for cluster VMs.", "bool", hclBool(true)},
	}
	for _, item := range mutableVars {
		variable := block("variable", item.name)
		variable.Body.Attr("description", str(item.description))
		variable.Body.Attr("type", expr(item.typeExpr))
		variable.Body.Attr("default", item.defaultVal)
		variablesDoc.AddBlock(variable)
	}

	locals := block("locals")
	locals.Body.Attr("cluster_name", str(cfg.Cluster.Name))
	locals.Body.Attr("cluster_id", str(cfg.Cluster.ID))
	locals.Body.Attr("name_prefix", str(cfg.Cluster.ID))
	locals.Body.Attr("aws_account_id", expr("data.aws_caller_identity.current.account_id"))
	locals.Body.Attr("aws_region", expr("data.aws_region.current.region"))
	locals.Body.Attr("netsy_bucket_name", str("${local.cluster_id}-${local.aws_account_id}-netsy"))
	locals.Body.Attr("registry_bucket_name", str("${local.cluster_id}-${local.aws_account_id}-registry"))
	locals.Body.Attr("oidc_issuer_url", str(cfg.Cluster.OIDC.IssuerURL))
	locals.Body.Attr("oidc_client_id", str(cfg.ResolvedClientID()))
	locals.Body.Attr("oidc_username_claim", str(cfg.ResolvedUsernameClaim()))
	groupsClaim := cfg.Cluster.OIDC.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	locals.Body.Attr("oidc_groups_claim", str(groupsClaim))
	locals.Body.Attr("kubernetes_api_hostname", str(resolvedAPIHostname(cfg)))
	locals.Body.Attr("kubernetes_api_port", num(resolvedAPIPort(cfg)))
	locals.Body.Attr("kubernetes_cluster_cidr", stringValueList(cfg.Cluster.Kubernetes.ClusterCIDR))
	locals.Body.Attr("kubernetes_service_cidr", stringValueList(cfg.Cluster.Kubernetes.ServiceCIDR))
	locals.Body.Attr("mutable_env", mutableEnvValue())
	locals.Body.Attr("userdata", userdataValue(cfg, opts, provider.Kind))
	locals.Body.Attr("certificates", nstanceCertificatesValue())
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
	apiURL.Body.Attr("value", str("https://${local.kubernetes_api_hostname}:${local.kubernetes_api_port}"))
	outputsDoc.AddBlock(apiURL)

	accountName := safeName("account", provider.Account, provider.Region)
	networkName := safeName("network", provider.Account, provider.Region)
	account := block("module", accountName)
	account.Body.Attr("source", str("nstance-dev/nstance/aws//modules/account"))
	account.Body.Attr("cluster", expr("module.cluster"))
	account.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	mainDoc.AddBlock(account)

	addPodplaneAWSStorageAndRoles(&mainDoc, accountName)

	network := block("module", networkName)
	network.Body.Attr("source", str("nstance-dev/nstance/aws//modules/network"))
	network.Body.Attr("cluster", expr("module.cluster"))
	network.Body.Attr("enable_ssm", expr("var.enable_ssm"))
	if provider.VPC.ID != "" {
		network.Body.Attr("vpc_id", str(provider.VPC.ID))
	} else {
		network.Body.Attr("vpc_cidr_ipv4", str(provider.VPC.V4CIDR))
	}
	if provider.VPC.V6CIDR == "auto" {
		network.Body.Attr("enable_ipv6", boolean(true))
	}
	network.Body.Attr("subnets", subnetsValue(provider))
	if lbs := loadBalancersValue(provider); len(lbs) > 0 {
		network.Body.Attr("load_balancers", lbs)
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

	if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
		seed := block("resource", "podplane_netsy_seed_s3", "cluster")
		seed.Body.Attr("cluster_config_path", str("${path.module}/"+filepath.Base(configPath)))
		seed.Body.Attr("bucket", expr("aws_s3_bucket.netsy.bucket"))
		seed.Body.Attr("region", expr("local.aws_region"))
		if provider.Profile != "" {
			seed.Body.Attr("profile", str(provider.Profile))
		}
		seed.Body.Attr("depends_on", list(expr("aws_s3_bucket.netsy")))
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
	registryReadOnlyRole.Body.Attr("value", expr("aws_iam_role.registry_read_only.arn"))
	outputsDoc.AddBlock(registryReadOnlyRole)

	registryReadWriteRole := block("output", "registry_read_write_role_arn")
	registryReadWriteRole.Body.Attr("value", expr("aws_iam_role.registry_read_write.arn"))
	outputsDoc.AddBlock(registryReadWriteRole)

	netsyRole := block("output", "netsy_role_arn")
	netsyRole.Body.Attr("value", expr("aws_iam_role.netsy.arn"))
	outputsDoc.AddBlock(netsyRole)
	return []File{
		{Name: "podplane.cluster.main.tf", Content: mainDoc.String()},
		{Name: "podplane.cluster.variables.tf", Content: variablesDoc.String()},
		{Name: "podplane.cluster.outputs.tf", Content: outputsDoc.String()},
	}
}

// addPodplaneAWSStorageAndRoles adds Podplane-owned S3 buckets and workload
// roles that are intentionally outside the Nstance account/network/shard modules.
func addPodplaneAWSStorageAndRoles(doc *hclDocument, accountName string) {
	for _, bucketName := range []string{"netsy", "registry"} {
		bucket := block("resource", "aws_s3_bucket", bucketName)
		bucket.Body.Attr("bucket", expr("local."+bucketName+"_bucket_name"))
		doc.AddBlock(bucket)

		publicAccess := block("resource", "aws_s3_bucket_public_access_block", bucketName)
		publicAccess.Body.Attr("bucket", expr("aws_s3_bucket."+bucketName+".id"))
		publicAccess.Body.Attr("block_public_acls", boolean(true))
		publicAccess.Body.Attr("block_public_policy", boolean(true))
		publicAccess.Body.Attr("ignore_public_acls", boolean(true))
		publicAccess.Body.Attr("restrict_public_buckets", boolean(true))
		doc.AddBlock(publicAccess)

		encryption := block("resource", "aws_s3_bucket_server_side_encryption_configuration", bucketName)
		encryption.Body.Attr("bucket", expr("aws_s3_bucket."+bucketName+".id"))
		rule := block("rule")
		applyDefault := block("apply_server_side_encryption_by_default")
		applyDefault.Body.Attr("sse_algorithm", str("AES256"))
		rule.Body.Block(applyDefault)
		encryption.Body.Block(rule)
		doc.AddBlock(encryption)
	}

	assume := block("data", "aws_iam_policy_document", "assume_from_knc")
	statement := block("statement")
	statement.Body.Attr("actions", stringValueList([]string{"sts:AssumeRole"}))
	principals := block("principals")
	principals.Body.Attr("type", str("AWS"))
	principals.Body.Attr("identifiers", list(expr("module."+accountName+".agent_iam_role_arn")))
	statement.Body.Block(principals)
	assume.Body.Block(statement)
	doc.AddBlock(assume)

	addIAMRoleWithInlinePolicy(doc, "netsy", "${local.name_prefix}-netsy", "data.aws_iam_policy_document.netsy.json")
	addIAMRoleWithInlinePolicy(doc, "registry_read_only", "${local.name_prefix}-registry-read-only", "data.aws_iam_policy_document.registry_read_only.json")
	addIAMRoleWithInlinePolicy(doc, "registry_read_write", "${local.name_prefix}-registry-read-write", "data.aws_iam_policy_document.registry_read_write.json")
	addPodplaneKNCPolicy(doc, accountName)

	addNetsyPolicyDocument(doc)
	addRegistryPolicyDocument(doc, "registry_read_only", false)
	addRegistryPolicyDocument(doc, "registry_read_write", true)
}

// addIAMRoleWithInlinePolicy adds an IAM role with the shared KNC trust policy
// and one inline policy document.
func addIAMRoleWithInlinePolicy(doc *hclDocument, name string, roleName string, policyDocument string) {
	role := block("resource", "aws_iam_role", name)
	role.Body.Attr("name", str(roleName))
	role.Body.Attr("assume_role_policy", expr("data.aws_iam_policy_document.assume_from_knc.json"))
	doc.AddBlock(role)

	policy := block("resource", "aws_iam_role_policy", name)
	policy.Body.Attr("name", str(roleName+"-policy"))
	policy.Body.Attr("role", expr("aws_iam_role."+name+".id"))
	policy.Body.Attr("policy", expr(policyDocument))
	doc.AddBlock(policy)
}

// addPodplaneKNCPolicy allows Podplane worker nodes to assume only the
// Podplane-generated workload roles they need.
func addPodplaneKNCPolicy(doc *hclDocument, accountName string) {
	policy := block("data", "aws_iam_policy_document", "podplane_knc")

	assumeWorkloadRoles := block("statement")
	assumeWorkloadRoles.Body.Attr("sid", str("AssumePodplaneWorkloadRoles"))
	assumeWorkloadRoles.Body.Attr("actions", stringValueList([]string{"sts:AssumeRole"}))
	assumeWorkloadRoles.Body.Attr("resources", list(
		expr("aws_iam_role.netsy.arn"),
		expr("aws_iam_role.registry_read_only.arn"),
		expr("aws_iam_role.registry_read_write.arn"),
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

// addNetsyPolicyDocument adds the Netsy object-storage read/write policy.
func addNetsyPolicyDocument(doc *hclDocument) {
	policy := block("data", "aws_iam_policy_document", "netsy")
	objectStatement := block("statement")
	objectStatement.Body.Attr("sid", str("NetsyS3ObjectOperations"))
	objectStatement.Body.Attr("actions", stringValueList([]string{
		"s3:GetObject",
		"s3:PutObject",
		"s3:DeleteObject",
		"s3:GetObjectAttributes",
		"s3:AbortMultipartUpload",
		"s3:ListMultipartUploadParts",
	}))
	objectStatement.Body.Attr("resources", list(expr("\"${aws_s3_bucket.netsy.arn}/*\"")))
	policy.Body.Block(objectStatement)

	bucketStatement := block("statement")
	bucketStatement.Body.Attr("sid", str("NetsyS3BucketOperations"))
	bucketStatement.Body.Attr("actions", stringValueList([]string{"s3:ListBucket", "s3:ListBucketMultipartUploads"}))
	bucketStatement.Body.Attr("resources", list(expr("aws_s3_bucket.netsy.arn")))
	policy.Body.Block(bucketStatement)
	doc.AddBlock(policy)
}

// addRegistryPolicyDocument adds a registry bucket policy. Read-only is used by
// host-level zot; read-write is used by the in-cluster registry component.
func addRegistryPolicyDocument(doc *hclDocument, name string, write bool) {
	policy := block("data", "aws_iam_policy_document", name)
	bucketStatement := block("statement")
	bucketStatement.Body.Attr("actions", stringValueList([]string{
		"s3:ListBucket",
		"s3:GetBucketLocation",
		"s3:ListBucketMultipartUploads",
	}))
	bucketStatement.Body.Attr("resources", list(expr("aws_s3_bucket.registry.arn")))
	policy.Body.Block(bucketStatement)

	objectActions := []string{"s3:GetObject", "s3:ListMultipartUploadParts"}
	if write {
		objectActions = append(objectActions, "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload")
	}
	objectStatement := block("statement")
	objectStatement.Body.Attr("actions", stringValueList(objectActions))
	objectStatement.Body.Attr("resources", list(expr("\"${aws_s3_bucket.registry.arn}/*\"")))
	policy.Body.Block(objectStatement)
	doc.AddBlock(policy)
}

// mutableEnvValue returns the Terraform expression for vmconfig mutable.env
// values generated from Terraform-managed resources and generated variables.
func mutableEnvValue() hclExpression {
	return expr(`{
  SSH_AUTHORIZED_KEYS = var.ssh_authorized_keys
  TELEMETRY_ENABLED = tostring(var.telemetry_enabled)
  TELEMETRY_LOG_CLOUDINIT = tostring(var.telemetry_log_cloudinit)
  TELEMETRY_LOG_SERVICES = var.telemetry_log_services
  TELEMETRY_OTLP_ENDPOINT = var.telemetry_otlp_endpoint
  TELEMETRY_S3_BUCKET = var.telemetry_s3_bucket
  TELEMETRY_S3_REGION = local.aws_region
  TELEMETRY_S3_ENDPOINT = var.telemetry_s3_endpoint
  TELEMETRY_S3_ASSUME_ROLE = var.telemetry_s3_assume_role
  TELEMETRY_S3_ACCESS_KEY_ID = var.telemetry_s3_access_key_id
  TELEMETRY_S3_SECRET_ACCESS_KEY = var.telemetry_s3_secret_access_key
  OIDC_ISSUER = local.oidc_issuer_url
  OIDC_CA_CERT = var.oidc_ca_cert
  KUBE_API_ETCD_SERVERS = var.kube_api_etcd_servers
  KUBE_API_PUBLIC_HOSTNAME = local.kubernetes_api_hostname
  KUBE_API_INTERNAL_LB_HOSTNAME = ""
  KUBE_API_PORT = tostring(local.kubernetes_api_port)
  KUBE_LOG_LEVEL = tostring(var.kube_log_level)
  AWS_S3_USE_PATH_STYLE = var.aws_s3_use_path_style
  NETSY_BUCKET = aws_s3_bucket.netsy.bucket
  NETSY_REGION = local.aws_region
  NETSY_ENDPOINT = var.netsy_endpoint
  NETSY_ASSUME_ROLE = aws_iam_role.netsy.arn
  NETSY_ACCESS_KEY_ID = var.netsy_access_key_id
  NETSY_SECRET_ACCESS_KEY = var.netsy_secret_access_key
  REGISTRY_ENABLED = tostring(var.registry_enabled)
  REGISTRY_BUCKET = aws_s3_bucket.registry.bucket
  REGISTRY_REGION = local.aws_region
  REGISTRY_ENDPOINT = var.registry_endpoint
  REGISTRY_ASSUME_ROLE = aws_iam_role.registry_read_only.arn
  REGISTRY_ACCESS_KEY_ID = var.registry_access_key_id
  REGISTRY_SECRET_ACCESS_KEY = var.registry_secret_access_key
  REGISTRY_HOSTNAME = var.registry_hostname
}`)
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
			identField("size", num(pool.Size)),
			identField("subnet_pool", str(poolName)),
			identField("instance_type", str(pool.InstanceType)),
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

// userdataValue renders the userdata templates shared by every Nstance shard.
func userdataValue(cfg *clusterconfig.ClusterConfig, opts ClusterOptions, providerKind string) hclObject {
	awsAccountID := ""
	googleProjectID := ""
	switch providerKind {
	case "aws":
		awsAccountID = "${local.aws_account_id}"
	case "google":
		googleProjectID = "${local.google_project_id}"
	default:
		panic(fmt.Errorf("cluster provider %q is not supported", providerKind))
	}

	fields := make([]hclObjectField, 0, len(cfg.Cluster.Pools))
	for _, poolName := range sortedKeys(cfg.Cluster.Pools) {
		pool := cfg.Cluster.Pools[poolName]
		kind := poolKind(poolName)
		manifest := opts.VMConfigManifests[kind+"/"+pool.Arch]
		if manifest == nil {
			panic(fmt.Errorf("missing vmconfig manifest %s/%s", kind, pool.Arch))
		}
		userdataTemplate, err := userdata.SourceForNstance(manifest, opts.DepsMirrorURL, providerKind, awsAccountID, googleProjectID, "${var.immutable_ssh_authorized_keys}")
		if err != nil {
			panic(err)
		}
		userdataTemplate = strings.ReplaceAll(userdataTemplate, "${", "$${")
		userdataTemplate = strings.ReplaceAll(userdataTemplate, "$${local.aws_account_id}", "${local.aws_account_id}")
		userdataTemplate = strings.ReplaceAll(userdataTemplate, "$${local.google_project_id}", "${local.google_project_id}")
		userdataTemplate = strings.ReplaceAll(userdataTemplate, "$${var.immutable_ssh_authorized_keys}", "${var.immutable_ssh_authorized_keys}")
		fields = append(fields, field(poolName, heredoc(userdataTemplate)))
	}
	return object(fields...)
}

// templatesValue converts cluster pools into Nstance instance templates.
func templatesValue(cfg *clusterconfig.ClusterConfig) hclObject {
	fields := make([]hclObjectField, 0, len(cfg.Cluster.Pools))
	for _, poolName := range sortedKeys(cfg.Cluster.Pools) {
		pool := cfg.Cluster.Pools[poolName]
		kind := poolKind(poolName)
		fields = append(fields, field(poolName, object(
			identField("kind", str(kind)),
			identField("arch", str(pool.Arch)),
			identField("vars", expr("local.mutable_env")),
			identField("userdata", inlineObject(
				identField("source", str("inline")),
				identField("encoding", str("base64")),
				identField("content", expr("base64encode(local.userdata["+quote(poolName)+"])")),
			)),
			identField("files", nstanceTemplateFilesValue(kind)),
			identField("args", inlineObject(
				identField("ImageId", str("{{ .Image.debian_13_"+pool.Arch+" }}")),
			)),
		)))
	}
	return object(fields...)
}

// nstanceCertificatesValue returns the certificate templates required by the
// vmconfig runtime files generated for Nstance-managed Podplane VMs.
func nstanceCertificatesValue() hclObject {
	fields := []hclObjectField{}
	for _, cert := range clusterspec.Certificates("{{ .Cluster.ID }}") {
		certFields := []hclObjectField{
			identField("kind", str(cert.Kind)),
			identField("cn", str(cert.CN)),
			identField("dns", stringValueList(cert.DNS)),
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
