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

// addPodplaneGooglePermissions grants bucket access and scoped default-backend
// secret access without reading or writing secret values through Terraform.
func addPodplaneGooglePermissions(doc *hclDocument, cfg *clusterconfig.ClusterConfig, accountName string) {
	agent := "serviceAccount:${module." + accountName + ".agent_iam_role_arn}"
	buckets := block("resource", "google_storage_bucket_iam_member", "podplane_agent")
	buckets.Body.Attr("for_each", expr("google_storage_bucket.podplane_cluster"))
	buckets.Body.Attr("bucket", expr("each.value.name"))
	buckets.Body.Attr("role", str("roles/storage.objectAdmin"))
	buckets.Body.Attr("member", str(agent))
	doc.AddBlock(buckets)

	server := block("resource", "google_storage_bucket_iam_member", "podplane_server_registry")
	server.Body.Attr("bucket", expr(`google_storage_bucket.podplane_cluster["registry"].name`))
	server.Body.Attr("role", str("roles/storage.objectViewer"))
	server.Body.Attr("member", str("serviceAccount:${module."+accountName+".server_iam_role_arn}"))
	doc.AddBlock(server)

	provider := cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider]
	if provider.Kind != "gcp" {
		return
	}
	prefix := provider.KeyPrefix
	if prefix == "" {
		prefix = cfg.Cluster.ID
	}
	key := block("resource", "google_secret_manager_secret_iam_member", "podplane_workload_ca_key")
	key.Body.Attr("project", str(provider.ProjectID))
	key.Body.Attr("secret_id", str(prefix+"_workload-ca-key"))
	key.Body.Attr("role", str("roles/secretmanager.secretAccessor"))
	key.Body.Attr("member", str(agent))
	key.Body.Attr("depends_on", list(expr("podplane_workload_ca_key.cluster")))
	doc.AddBlock(key)

	// Tunnel ingress uses node certificates, not operator-owned secret bundles.
	if cfg.Cluster.Tunnel != nil || len(cfg.Cluster.Domains) == 0 {
		return
	}
	role := block("resource", "google_project_iam_custom_role", "podplane_ingress_certificates")
	role.Body.Attr("project", str(provider.ProjectID))
	role.Body.Attr("role_id", str("podplane_"+safeName(cfg.Cluster.ID)+"_ingress_certificates"))
	role.Body.Attr("title", str("Podplane ingress certificate versions"))
	role.Body.Attr("permissions", stringValueList([]string{
		"secretmanager.secrets.get",
		"secretmanager.versions.get",
		"secretmanager.versions.access",
		"secretmanager.versions.add",
	}))
	doc.AddBlock(role)

	member := block("resource", "google_project_iam_member", "podplane_ingress_certificates")
	member.Body.Attr("project", str(provider.ProjectID))
	member.Body.Attr("role", expr("google_project_iam_custom_role.podplane_ingress_certificates.id"))
	member.Body.Attr("member", str(agent))
	condition := block("condition")
	condition.Body.Attr("title", str("PodplaneIngressCertificateSecrets"))
	condition.Body.Attr("expression", str("resource.name.startsWith("+quote("projects/"+provider.ProjectID+"/secrets/"+prefix+"_platform-cluster_ingress-certificates_")+")"))
	member.Body.Block(condition)
	doc.AddBlock(member)

	// Google authorizes creation on the project; keep it separate from secret access.
	createRole := block("resource", "google_project_iam_custom_role", "podplane_ingress_create")
	createRole.Body.Attr("project", str(provider.ProjectID))
	createRole.Body.Attr("role_id", str("podplane_"+safeName(cfg.Cluster.ID)+"_ingress_create"))
	createRole.Body.Attr("title", str("Podplane ingress secret creation"))
	createRole.Body.Attr("permissions", stringValueList([]string{"secretmanager.secrets.create"}))
	doc.AddBlock(createRole)
	createMember := block("resource", "google_project_iam_member", "podplane_ingress_create")
	createMember.Body.Attr("project", str(provider.ProjectID))
	createMember.Body.Attr("role", expr("google_project_iam_custom_role.podplane_ingress_create.id"))
	createMember.Body.Attr("member", str(agent))
	doc.AddBlock(createMember)
}

// googleGenerationError checks contracts the main renderer must check before
// generating Google resources. A DNS zone's DNS suffix or numeric ID cannot be
// substituted for the managed-zone resource name required by the lookup.
func googleGenerationError(cfg *clusterconfig.ClusterConfig) error {
	provider := cfg.Cluster.Secrets.Providers[cfg.Cluster.Secrets.DefaultProvider]
	if provider.Kind == "gcp" && provider.Location != "" {
		return fmt.Errorf("regional Secret Manager location %q is not supported by Google Terraform generation; use a global gcp secrets backend", provider.Location)
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
