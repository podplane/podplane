// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"fmt"

	"github.com/podplane/podplane/internal/clusterconfig"
)

const (
	// AWSProviderSource is the registry address of the AWS provider used by cluster infrastructure.
	AWSProviderSource = "hashicorp/aws"
	// AWSProviderVersion is the supported AWS provider version constraint.
	AWSProviderVersion = ">= 6.0"
)

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
	serverRegistry := block("resource", "aws_iam_role_policy", "podplane_server_registry")
	serverRegistry.Body.Attr("name", str("${local.name_prefix}-server-registry-read-only"))
	serverRegistry.Body.Attr("role", expr("basename(module."+accountName+".server_iam_role_arn)"))
	serverRegistry.Body.Attr("policy", expr("data.aws_iam_policy_document.podplane_cluster[\"registry-read-only\"].json"))
	doc.AddBlock(serverRegistry)

	if route53ACMEEnabled(cfg) {
		addACMERoute53Role(doc)
	}
	addPodplaneKNCPolicy(doc, cfg, accountName)
}

// addACMERoute53Role adds the least-privilege role assumed for DNS-01.
func addACMERoute53Role(doc *hclDocument) {
	role := block("resource", "aws_iam_role", "acme_route53")
	role.Body.Attr("count", num(1))
	role.Body.Attr("name", str("${local.name_prefix}-acme-route53"))
	role.Body.Attr("assume_role_policy", expr("data.aws_iam_policy_document.assume_from_knc.json"))
	doc.AddBlock(role)

	policyDocument := block("data", "aws_iam_policy_document", "acme_route53")
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
	getChange.Body.Attr("resources", stringValueList([]string{"arn:${data.aws_partition.current.partition}:route53:::change/*"}))
	policyDocument.Body.Block(getChange)
	doc.AddBlock(policyDocument)

	policy := block("resource", "aws_iam_role_policy", "acme_route53")
	policy.Body.Attr("count", num(1))
	policy.Body.Attr("name", str("${local.name_prefix}-acme-route53-policy"))
	policy.Body.Attr("role", expr("aws_iam_role.acme_route53[0].id"))
	policy.Body.Attr("policy", expr("data.aws_iam_policy_document.acme_route53.json"))
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
		resources = append(resources, expr("aws_iam_role.acme_route53[0].arn"))
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

	provider := cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider]
	if provider.Kind != "aws" {
		return
	}
	prefix := provider.KeyPrefix
	if prefix == "" {
		prefix = cfg.Cluster.ID
	}
	keyPolicy := block("data", "aws_iam_policy_document", "podplane_workload_ca_key")
	readKey := block("statement")
	readKey.Body.Attr("sid", str("ReadPodplaneWorkloadCAKey"))
	region := "${local.aws_region}"
	if provider.Region != "" {
		region = provider.Region
	}
	if provider.ObjectType == "secretsmanager" {
		readKey.Body.Attr("actions", stringValueList([]string{"secretsmanager:GetSecretValue"}))
		readKey.Body.Attr("resources", list(str("arn:${data.aws_partition.current.partition}:secretsmanager:"+region+":${local.aws_account_id}:secret:/"+prefix+"/workload-ca-key-*")))
	} else {
		readKey.Body.Attr("actions", stringValueList([]string{"ssm:GetParameter"}))
		readKey.Body.Attr("resources", list(str("arn:${data.aws_partition.current.partition}:ssm:"+region+":${local.aws_account_id}:parameter/"+prefix+"/workload-ca-key")))
	}
	keyPolicy.Body.Block(readKey)
	if len(cfg.Cluster.Domains) > 0 && cfg.Cluster.Tunnel == nil {
		ingressCertificates := block("statement")
		ingressCertificates.Body.Attr("sid", str("ManagePodplaneIngressCertificates"))
		if provider.ObjectType == "secretsmanager" {
			ingressCertificates.Body.Attr("actions", stringValueList([]string{"secretsmanager:CreateSecret", "secretsmanager:DescribeSecret", "secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue"}))
			ingressCertificates.Body.Attr("resources", list(str("arn:${data.aws_partition.current.partition}:secretsmanager:"+region+":${local.aws_account_id}:secret:/"+prefix+"/platform-cluster/ingress-certificates/*")))
		} else {
			ingressCertificates.Body.Attr("actions", stringValueList([]string{"ssm:GetParameter", "ssm:PutParameter"}))
			ingressCertificates.Body.Attr("resources", list(str("arn:${data.aws_partition.current.partition}:ssm:"+region+":${local.aws_account_id}:parameter/"+prefix+"/platform-cluster/ingress-certificates/*")))
		}
		keyPolicy.Body.Block(ingressCertificates)
	}
	doc.AddBlock(keyPolicy)
	keyRolePolicy := block("resource", "aws_iam_role_policy", "podplane_workload_ca_key")
	keyRolePolicy.Body.Attr("name", str("${local.name_prefix}-workload-ca-key"))
	keyRolePolicy.Body.Attr("role", expr("module."+accountName+".agent_iam_role_name"))
	keyRolePolicy.Body.Attr("policy", expr("data.aws_iam_policy_document.podplane_workload_ca_key.json"))
	doc.AddBlock(keyRolePolicy)
}

// route53ACMEEnabled reports whether generated infrastructure needs a DNS-01 role.
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
