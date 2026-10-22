// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"fmt"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// addPodplaneGoogleBuckets adds private buckets using ambient Google credentials.
func addPodplaneGoogleBuckets(doc *hclDocument) {
	bucket := block("resource", "google_storage_bucket", "podplane_cluster")
	bucket.Body.Attr("for_each", object(
		field("netsy", expr("local.netsy_bucket_name")),
		field("registry", expr("local.registry_bucket_name")),
	))
	bucket.Body.Attr("name", expr("each.value"))
	bucket.Body.Attr("project", expr("local.provider_project"))
	bucket.Body.Attr("location", expr("local.provider_region"))
	bucket.Body.Attr("uniform_bucket_level_access", boolean(true))
	bucket.Body.Attr("public_access_prevention", str("enforced"))
	doc.AddBlock(bucket)
}

// addPodplaneGooglePermissions separates host bucket access from federated Pod access.
func addPodplaneGooglePermissions(doc *hclDocument, cfg *clusterconfig.ClusterConfig, accountName string) {
	agent := "serviceAccount:${module." + accountName + ".agent_iam_role_arn}"
	buckets := block("resource", "google_storage_bucket_iam_member", "podplane_agent")
	buckets.Body.Attr("for_each", expr("google_storage_bucket.podplane_cluster"))
	buckets.Body.Attr("bucket", expr("each.value.name"))
	buckets.Body.Attr("role", expr(`each.key == "registry" ? "roles/storage.objectViewer" : "roles/storage.objectAdmin"`))
	buckets.Body.Attr("member", str(agent))
	doc.AddBlock(buckets)

	server := block("resource", "google_storage_bucket_iam_member", "podplane_server_registry")
	server.Body.Attr("bucket", expr(`google_storage_bucket.podplane_cluster["registry"].name`))
	server.Body.Attr("role", str("roles/storage.objectViewer"))
	server.Body.Attr("member", str("serviceAccount:${module."+accountName+".server_iam_role_arn}"))
	doc.AddBlock(server)

	addGoogleWorkloadIdentity(doc)
	addGoogleSecretsPermissions(doc, cfg)
}

// googleWorkloadPrincipal identifies one Kubernetes ServiceAccount, not an entire namespace.
func googleWorkloadPrincipal(namespace, name string) hclValue {
	return str("principal://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/subject/system:serviceaccount:" + namespace + ":" + name)
}

// addGoogleWorkloadIdentity trusts static public keys without public API discovery.
func addGoogleWorkloadIdentity(doc *hclDocument) {
	sts := block("resource", "google_project_service", "workload_identity")
	sts.Body.Attr("project", expr("local.provider_project"))
	sts.Body.Attr("service", str("sts.googleapis.com"))
	sts.Body.Attr("disable_on_destroy", boolean(false))
	doc.AddBlock(sts)

	pool := block("resource", "google_iam_workload_identity_pool", "cluster")
	pool.Body.Attr("project", expr("local.provider_project"))
	pool.Body.Attr("workload_identity_pool_id", str("pp-${substr(sha256(module.cluster.bucket), 0, 24)}"))
	pool.Body.Attr("display_name", expr(`substr("Podplane ${local.cluster_id}", 0, 32)`))
	pool.Body.Attr("depends_on", list(expr("google_project_service.workload_identity")))
	doc.AddBlock(pool)

	provider := block("resource", "google_iam_workload_identity_pool_provider", "cluster")
	provider.Body.Attr("project", expr("local.provider_project"))
	provider.Body.Attr("workload_identity_pool_id", expr("google_iam_workload_identity_pool.cluster.workload_identity_pool_id"))
	provider.Body.Attr("workload_identity_pool_provider_id", str("kubernetes"))
	provider.Body.Attr("attribute_mapping", object(field("google.subject", str("assertion.sub"))))
	provider.Body.Attr("attribute_condition", str("assertion.sub.startsWith('system:serviceaccount:')"))
	oidc := block("oidc")
	oidc.Body.Attr("issuer_uri", str("https://${var.kubernetes_api_hostname}:${var.kubernetes_api_port}"))
	oidc.Body.Attr("allowed_audiences", list(str("https://iam.googleapis.com/${google_iam_workload_identity_pool.cluster.name}/providers/kubernetes")))
	oidc.Body.Attr("jwks_json", expr("podplane_service_account_key.cluster.jwks_json"))
	provider.Body.Block(oidc)
	doc.AddBlock(provider)

	writer := block("resource", "google_storage_bucket_iam_member", "podplane_registry_writer")
	writer.Body.Attr("bucket", expr(`google_storage_bucket.podplane_cluster["registry"].name`))
	writer.Body.Attr("role", str("roles/storage.objectAdmin"))
	writer.Body.Attr("member", googleWorkloadPrincipal("platform-zot-registry", "zot-registry"))
	doc.AddBlock(writer)
}

// addGoogleSecretsPermissions separates lifecycle management from shared CSI value reads.
func addGoogleSecretsPermissions(doc *hclDocument, cfg *clusterconfig.ClusterConfig) {
	providers := []hclObjectField{}
	for _, name := range sortedKeys(cfg.Cluster.Secrets.Providers) {
		p := cfg.Cluster.Secrets.Providers[name]
		if p.Kind != "gcp" {
			continue
		}
		prefix := p.KeyPrefix
		if prefix == "" {
			prefix = cfg.Cluster.ID
		}
		providers = append(providers, field(name, object(identField("project", str(p.ProjectID)), identField("prefix", str(prefix)))))
	}
	locals := block("locals")
	locals.Body.Attr("google_secrets_providers", object(providers...))
	doc.AddBlock(locals)
	project := block("data", "google_project", "secrets")
	project.Body.Attr("for_each", expr("local.google_secrets_providers"))
	project.Body.Attr("project_id", expr("each.value.project"))
	doc.AddBlock(project)

	// Creation and listing are authorized on the project; resource-name conditions
	// cannot scope them. Value access and lifecycle writes are scoped separately.
	prefix := "projects/${data.google_project.secrets[each.key].number}/secrets/${each.value.prefix}_"
	clusterSecrets := "resource.name.startsWith(" + quote(prefix) + ")"
	for _, grant := range []struct {
		name, namespace, account, condition string
		permissions                         []string
	}{
		{"secrets_catalog", "platform-podplane-operator", "platform-podplane-operator", "", []string{"secretmanager.secrets.create", "secretmanager.secrets.list"}},
		{"secrets_lifecycle", "platform-podplane-operator", "platform-podplane-operator", clusterSecrets + " && resource.name != " + quote(prefix+"workload-ca-key") + " && !resource.name.startsWith(" + quote(prefix+"workload-ca-key/") + ")", []string{"secretmanager.secrets.get", "secretmanager.secrets.delete", "secretmanager.versions.get", "secretmanager.versions.list", "secretmanager.versions.add", "secretmanager.versions.disable", "secretmanager.versions.enable", "secretmanager.versions.destroy"}},
		{"secrets_reader", "platform-secrets-store-csi-provider-gcp", "platform-secrets-store-csi-provider-gcp", clusterSecrets, []string{"secretmanager.versions.access"}},
		{"ingress_state", "platform-podplane-operator", "platform-podplane-operator", "resource.name.startsWith(" + quote(prefix+"platform-cluster_ingress-certificates_") + ")", []string{"secretmanager.versions.access"}},
	} {
		name := "podplane_" + grant.name
		role := block("resource", "google_project_iam_custom_role", name)
		role.Body.Attr("for_each", expr("local.google_secrets_providers"))
		role.Body.Attr("project", expr("each.value.project"))
		role.Body.Attr("role_id", expr(`"pp_${substr(sha256(join("/", [module.cluster.bucket, each.key])), 0, 20)}_`+grant.name+`"`))
		role.Body.Attr("title", str("Podplane "+grant.name))
		role.Body.Attr("permissions", stringValueList(grant.permissions))
		doc.AddBlock(role)
		member := block("resource", "google_project_iam_member", name)
		member.Body.Attr("for_each", expr("local.google_secrets_providers"))
		member.Body.Attr("project", expr("each.value.project"))
		member.Body.Attr("role", expr("google_project_iam_custom_role."+name+"[each.key].id"))
		member.Body.Attr("member", googleWorkloadPrincipal(grant.namespace, grant.account))
		if grant.condition != "" {
			condition := block("condition")
			condition.Body.Attr("title", str("PodplaneSecretScope"))
			condition.Body.Attr("expression", str(grant.condition))
			member.Body.Block(condition)
		}
		doc.AddBlock(member)
	}
}

// googleGenerationError checks contracts the main renderer must check before
// generating Google resources. A DNS zone's DNS suffix or numeric ID cannot be
// substituted for the managed-zone resource name required by the lookup.
func googleGenerationError(cfg *clusterconfig.ClusterConfig) error {
	for _, provider := range cfg.Cluster.Secrets.Providers {
		if provider.Kind == "gcp" && provider.Location != "" {
			return fmt.Errorf("regional Secret Manager location %q is not supported by Google Terraform generation; use a global gcp secrets backend", provider.Location)
		}
	}
	if cfg.Cluster.Tunnel != nil {
		return nil
	}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider != nil && domain.Provider.Kind == "google-cloud-dns" && domain.Provider.HostedZoneName == "" {
			return fmt.Errorf("domain %s: google-cloud-dns requires provider.hosted_zone_name (the managed-zone resource name); it cannot be inferred from the DNS suffix or hosted_zone_id", domain.Zone)
		}
	}
	return nil
}

// addGoogleDNS adds apex, wildcard and managed API A records from Nstance NLB
// frontend IPs. Tunnel DNS is user-managed. Call googleGenerationError first.
func addGoogleDNS(doc *hclDocument, cfg *clusterconfig.ClusterConfig, networkName string) {
	if cfg.Cluster.Tunnel != nil {
		return
	}
	zones := []hclObjectField{}
	records := map[string]hclValue{}
	for _, domain := range cfg.Cluster.Domains {
		if domain.Provider == nil || domain.Provider.Kind != "google-cloud-dns" {
			continue
		}
		var project hclValue = expr("local.provider_project")
		if domain.Provider.Project != "" {
			project = str(domain.Provider.Project)
		}
		zones = append(zones, field(domain.Zone, object(
			identField("name", str(domain.Provider.HostedZoneName)),
			identField("project", project),
		)))
		record := object(
			identField("zone", str(domain.Zone)),
			identField("load_balancer", str(domain.ResolvedDomainLoadBalancer())),
		)
		records[domain.Zone] = record
		records["*."+domain.Zone] = record
	}
	if len(zones) == 0 {
		return
	}
	if index, managed := managedAPIDomain(cfg); managed && !apiRecordCoveredByDomain(cfg) {
		domain := cfg.Cluster.Domains[index]
		if domain.Provider.Kind == "google-cloud-dns" {
			records[cfg.Cluster.Kubernetes.APIHostname] = object(
				identField("zone", str(domain.Zone)),
				identField("load_balancer", str(cfg.Cluster.Kubernetes.APILoadBalancer)),
			)
		}
	}
	fields := []hclObjectField{}
	for _, name := range sortedKeys(records) {
		fields = append(fields, field(name, records[name]))
	}
	locals := block("locals")
	locals.Body.Attr("google_managed_dns_zones", object(zones...))
	locals.Body.Attr("google_managed_dns_records", object(fields...))
	doc.AddBlock(locals)

	zone := block("data", "google_dns_managed_zone", "managed")
	zone.Body.Attr("for_each", expr("local.google_managed_dns_zones"))
	zone.Body.Attr("name", expr("each.value.name"))
	zone.Body.Attr("project", expr("each.value.project"))
	doc.AddBlock(zone)

	record := block("resource", "google_dns_record_set", "managed_ipv4")
	record.Body.Attr("for_each", expr("local.google_managed_dns_records"))
	record.Body.Attr("project", expr("data.google_dns_managed_zone.managed[each.value.zone].project"))
	record.Body.Attr("managed_zone", expr("data.google_dns_managed_zone.managed[each.value.zone].name"))
	record.Body.Attr("name", expr(`"${trimsuffix(each.key, ".")}."`))
	record.Body.Attr("type", str("A"))
	record.Body.Attr("ttl", num(300))
	record.Body.Attr("rrdatas", expr(fmt.Sprintf("distinct([for frontend in module.%s.load_balancers[each.value.load_balancer].frontends : frontend.ip])", networkName)))
	doc.AddBlock(record)
}
