// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/pkg/seeds"
)

// tunnelImage resolves the provider's runtime image from the target VM manifest.
func tunnelImage(kind string, manifest VMConfigManifest) (string, error) {
	if kind != "cloudflare" {
		return "", fmt.Errorf("unsupported tunnel provider %q", kind)
	}
	var pinned deps.Manifest
	if err := json.Unmarshal(manifest.JSON, &pinned); err != nil {
		return "", fmt.Errorf("parse vmconfig manifest %s/%s: %w", manifest.Kind, manifest.Arch, err)
	}
	for _, image := range pinned.VMConfig.Images {
		ref, err := name.ParseReference(image.Image)
		if err != nil {
			return "", fmt.Errorf("parse vmconfig image %q: %w", image.Image, err)
		}
		if ref.Context().Name() != "index.docker.io/cloudflare/cloudflared" {
			continue
		}
		digest, err := name.NewDigest(ref.Context().Name() + "@" + image.Digest)
		if err != nil {
			return "", fmt.Errorf("pin tunnel image for %s/%s: %w", manifest.Kind, manifest.Arch, err)
		}
		return digest.Name(), nil
	}
	return "", fmt.Errorf("vmconfig manifest %s/%s has no image for tunnel provider %q", manifest.Kind, manifest.Arch, kind)
}

// serverArch matches the cloud-specific Nstance server instance defaults.
func serverArch(provider clusterconfig.Provider) string {
	if provider.Kind == "google" {
		return "amd64"
	}
	return "arm64"
}

// serverEnvValue supplies shard settings without depending on the shard module itself.
func serverEnvValue(cfg *clusterconfig.ClusterConfig, zone string) hclValue {
	provider := cfg.Cluster.Providers[0]
	storage := "s3"
	if provider.Kind == "google" {
		storage = "gcs"
	}
	settings := object(
		field("CLUSTER_ID", expr("local.cluster_id")),
		field("PROVIDER_REGION", expr("local.provider_region")),
		field("PROVIDER_ZONE", str(zone)),
		field("NSTANCE_SERVER_STORAGE", str(storage)),
		field("NSTANCE_SERVER_BUCKET", expr("module.cluster.bucket")),
		field("NSTANCE_SERVER_SHARD", str(zone)),
		field("NSTANCE_SERVER_PREFIX", str("shard/"+zone+"/")),
		field("NSTANCE_SLEEP_ENABLED", str(strconv.FormatBool(seeds.HasPlatformComponents(cfg.Cluster.Seed.Name)))),
		field("NSTANCE_TUNNEL_ENABLED", str(strconv.FormatBool(cfg.Cluster.Tunnel != nil && seeds.HasPlatformComponents(cfg.Cluster.Seed.Name)))),
		field("NSTANCE_TUNNEL_STORAGE", str(storage)),
		field("NSTANCE_TUNNEL_BUCKET", expr("module.cluster.bucket")),
		field("NSTANCE_TUNNEL_SHARD", str(zone)),
		field("NSTANCE_TUNNEL_PREFIX", str("shard/"+zone+"/")),
	)
	return expr("merge(local.host_env, " + settings.renderHCL(0) + ")")
}

// activityPorts returns the ports monitored for activity when deciding whether a pool can sleep.
func activityPorts(cfg *clusterconfig.ClusterConfig, pool string) string {
	if !seeds.HasPlatformComponents(cfg.Cluster.Seed.Name) {
		return ""
	}
	ports := []int{}
	if cfg.Cluster.Tunnel != nil {
		if pool == "control-plane" {
			ports = append(ports, 6443)
			if len(cfg.Cluster.Domains) > 0 {
				ports = append(ports, 443)
			}
		}
	} else {
		for _, lb := range cfg.Cluster.Providers[0].LoadBalancers {
			for _, listener := range lb.Listeners {
				if listener.Pool == pool {
					ports = append(ports, listener.ResolvedTargetPort())
				}
			}
		}
	}
	slices.Sort(ports)
	values := []string{}
	for _, port := range slices.Compact(ports) {
		values = append(values, strconv.Itoa(port))
	}
	return strings.Join(values, ",")
}

// natValue passes deployment choices through, resolving optional reserved addresses by shard.
func natValue(nat *clusterconfig.NAT, zone, networkName string) hclObject {
	fields := []hclObjectField{}
	for _, tenant := range sortedKeys(nat.Tenants) {
		policy := nat.Tenants[tenant]
		settings := []hclObjectField{
			identField("group", str(policy.Group)),
			identField("instance_type_ladder", stringValueList(policy.InstanceTypeLadder)),
		}
		if nat.FixedPublicIPv4Count > 0 {
			key := nat.Groups[policy.Group].SubnetPool + "-" + zone
			settings = append(settings, identField("public_addresses", expr("module."+networkName+".nat_public_addresses["+quote(key)+"]")))
		}
		fields = append(fields, field(tenant, object(settings...)))
	}
	return object(fields...)
}

// addNATSubnetLookups resolves node CIDRs for existing subnets without allowing the whole VPC.
func addNATSubnetLookups(doc *hclDocument, provider clusterconfig.Provider) {
	ids := []string{}
	for _, subnets := range provider.Zones {
		for _, subnet := range subnets {
			if subnet.Pool != "" && subnet.ID != "" {
				ids = append(ids, subnet.ID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	slices.Sort(ids)
	dataType := "aws_subnet"
	if provider.Kind == "google" {
		dataType = "google_compute_subnetwork"
	}
	subnet := block("data", dataType, "nat_sources")
	subnet.Body.Attr("for_each", expr("toset("+stringValueList(slices.Compact(ids)).renderHCL(0)+")"))
	if provider.Kind == "google" {
		subnet.Body.Attr("name", expr("basename(each.value)"))
		subnet.Body.Attr("project", expr("local.provider_project"))
		subnet.Body.Attr("region", expr("local.provider_region"))
	} else {
		subnet.Body.Attr("id", expr("each.value"))
	}
	doc.AddBlock(subnet)
}

// natSourceCIDRs allows only the configured node subnet ranges.
func natSourceCIDRs(provider clusterconfig.Provider) hclValue {
	cidrs := []string{}
	existing := false
	for _, subnets := range provider.Zones {
		for _, subnet := range subnets {
			if subnet.Pool == "" {
				continue
			}
			if subnet.ID != "" {
				existing = true
				continue
			}
			cidrs = append(cidrs, subnet.V4CIDR)
		}
	}
	slices.Sort(cidrs)
	cidrs = slices.Compact(cidrs)
	if !existing {
		return str(strings.Join(cidrs, ","))
	}
	source := "data.aws_subnet.nat_sources"
	attribute := "cidr_block"
	if provider.Kind == "google" {
		source, attribute = "data.google_compute_subnetwork.nat_sources", "ip_cidr_range"
	}
	return expr("join(\",\", distinct(concat(" + stringValueList(cidrs).renderHCL(0) + ", [for subnet in " + source + " : subnet." + attribute + "])))")
}

// endpointTarget returns the cloud frontend or existing tunnel's DNS target.
func endpointTarget(cfg *clusterconfig.ClusterConfig, networkName, loadBalancer string) hclValue {
	if cfg.Cluster.Tunnel != nil {
		return str(cfg.Cluster.Tunnel.ID + ".cfargotunnel.com")
	}
	attribute := "dns_name"
	if cfg.Cluster.Providers[0].Kind == "google" {
		attribute = "frontends[0].ip"
	}
	return expr("module." + networkName + ".load_balancers[" + quote(loadBalancer) + "]." + attribute)
}

// imageArgs supplies cloud-specific Debian image and optional root-disk settings.
func imageArgs(providerKind, arch string, diskSize int) hclObject {
	if providerKind == "google" {
		image := "projects/debian-cloud/global/images/family/debian-13"
		if arch == "arm64" {
			image += "-arm64"
		}
		fields := []hclObjectField{identField("SourceImage", str(image))}
		if diskSize > 0 {
			fields = append(fields, identField("DiskSizeGb", num(diskSize)))
		}
		return object(fields...)
	}
	fields := []hclObjectField{identField("ImageId", str("{{ .Image.debian_13_"+arch+" }}"))}
	if diskSize > 0 {
		fields = append(fields, identField("BlockDeviceMappings", list(inlineObject(
			identField("DeviceName", str("/dev/sda1")),
			identField("Ebs", inlineObject(
				identField("VolumeSize", num(diskSize)), identField("VolumeType", str("gp3")),
				identField("Encrypted", boolean(true)), identField("DeleteOnTermination", boolean(true)),
			)),
		))))
	}
	return object(fields...)
}

// tunnelConfig routes public HTTPS to verified API and ingress origins without a port-80 route.
func tunnelConfig(cfg *clusterconfig.ClusterConfig, wake bool) hclObject {
	apiPort, ingressPort := 6443, 443
	if wake {
		apiPort, ingressPort = 16443, 10443
	}
	// route keeps the origin's DNS identity independent of the local TCP destination.
	route := func(hostname hclValue, identity hclValue, port int) hclValue {
		return object(
			identField("hostname", hostname),
			identField("service", str(fmt.Sprintf("https://127.0.0.1:%d", port))),
			identField("originRequest", object(
				identField("caPool", str("/run/tunnel/ca.crt")),
				identField("originServerName", identity),
			)),
		)
	}
	routes := list(route(expr("var.kubernetes_api_hostname"), expr("var.kubernetes_api_hostname"), apiPort))
	for _, domain := range cfg.Cluster.Domains {
		routes = append(routes, route(str(domain.Zone), str(domain.Zone), ingressPort), route(str("*."+domain.Zone), str(domain.Zone), ingressPort))
	}
	routes = append(routes, object(identField("service", str("http_status:404"))))
	return object(
		field("tunnel", str(cfg.Cluster.Tunnel.ID)),
		field("credentials-file", str("/run/tunnel/credentials.json")),
		field("metrics", str("127.0.0.1:2000")),
		field("ingress", routes),
	)
}

// nodeTunnelFiles supplies protected, agent-delivered files for tunnels on control-plane VMs.
func nodeTunnelFiles(cfg *clusterconfig.ClusterConfig, image string) []hclObjectField {
	return []hclObjectField{
		field("tunnel.env", object(
			identField("kind", str("env")),
			identField("template", object(
				identField("TUNNEL_ENABLED", str("true")), identField("TUNNEL_IMAGE", str(image)),
				identField("TUNNEL_ARGS", str("tunnel|--config|/run/tunnel/config.json|run")),
				identField("TUNNEL_FILES", str("tunnel-config.json:/run/tunnel/config.json|tunnel-credentials.json:/run/tunnel/credentials.json|tunnel-ca.crt:/run/tunnel/ca.crt")),
			)),
		)),
		field("tunnel-config.json", object(identField("kind", str("json")), identField("template", tunnelConfig(cfg, false)))),
		field("tunnel-credentials.json", object(identField("kind", str("secret")), identField("source", str(cfg.Cluster.Tunnel.CredentialSecret)))),
		field("tunnel-ca.crt", object(identField("kind", str("string")), identField("template", str("{{ .Cluster.CACert }}")))),
	}
}

// wakeTunnelsValue supplies API/ingress listeners and tunnel runtime files for NST instances.
func wakeTunnelsValue(cfg *clusterconfig.ClusterConfig, image string) hclObject {
	listeners := list(inlineObject(identField("target_port", num(6443)), identField("proxy_port", num(16443))))
	if len(cfg.Cluster.Domains) > 0 {
		listeners = append(listeners, inlineObject(identField("target_port", num(443)), identField("proxy_port", num(10443))))
	}
	return object(field("cluster", object(
		identField("listeners", listeners),
		identField("image", str(image)),
		identField("args", stringValueList([]string{"tunnel", "--config", "/run/tunnel/config.json", "run"})),
		identField("readiness_url", str("http://127.0.0.1:2000/ready")),
		identField("files", object(
			field("/run/tunnel/config.json", object(identField("kind", str("json")), identField("template", tunnelConfig(cfg, true)))),
			field("/run/tunnel/credentials.json", object(identField("kind", str("secret")), identField("source", str(cfg.Cluster.Tunnel.CredentialSecret)))),
			field("/run/tunnel/ca.crt", object(identField("kind", str("storage")), identField("source", str("tunnel-ca.crt")))),
		)),
	)))
}

// addTunnelCAFiles copies only the public CA into the shard-scoped tunnel file store.
// Seed bootstrap has already waited for initial CA publication; no private material is read.
func addTunnelCAFiles(doc *hclDocument, provider clusterconfig.Provider) {
	resourceType, keyName := "aws_s3_object_copy", "key"
	if provider.Kind == "google" {
		resourceType, keyName = "google_storage_bucket_object", "name"
		ca := block("data", "google_storage_bucket_object_content", "nstance_ca")
		ca.Body.Attr("bucket", expr("module.cluster.bucket"))
		ca.Body.Attr("name", str("cluster/ca.crt"))
		ca.Body.Attr("depends_on", list(expr("podplane_netsy_seed_gcs.cluster")))
		doc.AddBlock(ca)
	}
	copy := block("resource", resourceType, "tunnel_ca")
	copy.Body.Attr("for_each", expr("toset("+stringValueList(sortedKeys(provider.Zones)).renderHCL(0)+")"))
	copy.Body.Attr("bucket", expr("module.cluster.bucket"))
	copy.Body.Attr(keyName, str("shard/${each.key}/tunnel-ca.crt"))
	if provider.Kind == "google" {
		copy.Body.Attr("content", expr("data.google_storage_bucket_object_content.nstance_ca.content"))
	} else {
		copy.Body.Attr("source", str("${module.cluster.bucket}/cluster/ca.crt"))
		copy.Body.Attr("depends_on", list(expr("podplane_netsy_seed_s3.cluster")))
	}
	doc.AddBlock(copy)
}

// cloudSigningKey configures a retained key in the same backend Nstance reads.
func cloudSigningKey(resourceType string, provider clusterconfig.Provider, prefix string) hclBlock {
	secretProvider := "aws-parameter-store"
	if provider.Kind == "google" {
		secretProvider = "google-secret-manager"
	}
	key := block("resource", resourceType, "cluster")
	key.Body.Attr("secrets_provider", str(secretProvider))
	key.Body.Attr("secret_prefix", str(prefix))
	if provider.Kind == "aws" {
		key.Body.Attr("region", expr("local.provider_region"))
		if provider.Profile != "" {
			key.Body.Attr("profile", str(provider.Profile))
		}
	} else {
		key.Body.Attr("project", expr("local.provider_project"))
	}
	key.Body.Attr("depends_on", list(expr("module.cluster")))
	return key
}

// addNstanceBootstrap orders metadata-only registration-key creation before seed bootstrap.
func addNstanceBootstrap(doc *hclDocument, seed *hclBlock, provider clusterconfig.Provider, prefix string) {
	secretProvider := "aws-parameter-store"
	if provider.Kind == "google" {
		secretProvider = "google-secret-manager"
	}
	key := cloudSigningKey("podplane_nstance_registration_key", provider, prefix)
	doc.AddBlock(key)
	seed.Body.Attr("nstance_bootstrap", object(
		identField("provider", str(secretProvider)), identField("secret_prefix", str(prefix)),
		identField("signing_key_id", expr("podplane_nstance_registration_key.cluster.id")),
		identField("signing_key_version", expr("podplane_nstance_registration_key.cluster.backend_version")),
		identField("signing_key_fingerprint", expr("podplane_nstance_registration_key.cluster.public_key_fingerprint")),
		identField("cluster_id", expr("local.cluster_id")), identField("tenant", str("default")),
		identField("namespace", str("platform-nstance-operator")), identField("ca_bucket", expr("module.cluster.bucket")),
	))
}

// seedValues supplies Terraform-resolved, non-sensitive component settings.
func seedValues(cfg *clusterconfig.ClusterConfig, provider clusterconfig.Provider) hclValue {
	values := []hclObjectField{}
	if seeds.HasPlatformComponents(cfg.Cluster.Seed.Name) {
		shards := []hclObjectField{}
		for _, zone := range sortedKeys(provider.Zones) {
			address := "module." + safeName("shard", zone) + ".server_ips[0]"
			shards = append(shards, field(zone, object(
				identField("registration_addr", expr("format(\"%s:8992\", "+address+")")),
				identField("operator_addr", expr("format(\"%s:8993\", "+address+")")),
			)))
		}
		values = append(values, field("nstance-operator", object(identField("config", object(identField("shards", object(shards...)))))))
	}
	if route53ACMEEnabled(cfg) {
		values = append(values, field("podplane-operator", expr(`{ podplane = { operator = { config = { ingressCertificates = {
  domains = { for name, zone in data.aws_route53_zone.managed : name => {
    dnsProvider = { kind = "aws-route53", hostedZoneID = zone.zone_id, region = local.aws_region, roleARN = aws_iam_role.acme_route53[0].arn }
  } }
} } } } }`)))
	}
	if provider.Kind == "aws" {
		values = append(values, field("zot-registry", expr(`{
  platform = { zotRegistry = { storage = { bucket = aws_s3_bucket.podplane_cluster["registry"].bucket, region = local.aws_region } } }
  zot = { podAnnotations = { "iam.amazonaws.com/role" = aws_iam_role.podplane_cluster["registry-read-write"].arn } }
}`)))
	}
	if provider.Kind == "google" {
		values = append(values, field("zot-registry", expr(`{
  platform = { zotRegistry = {
    googleWorkloadIdentity = { provider = google_iam_workload_identity_pool_provider.cluster.name }
    storage = {
      driver = "gcs"
      bucket = google_storage_bucket.podplane_cluster["registry"].name
    }
  } }
  zot = {
    serviceAccount = { create = true, name = "zot-registry" }
    env = [{ name = "GOOGLE_APPLICATION_CREDENTIALS", value = "/var/run/google/identity/credentials.json" }]
    extraVolumes = [
      { name = "podplane-zot-config", configMap = { name = "zot-registry-config" } },
      { name = "google-identity", projected = { sources = [
        { serviceAccountToken = { audience = "https://iam.googleapis.com/${google_iam_workload_identity_pool_provider.cluster.name}", expirationSeconds = 3600, path = "token" } },
        { configMap = { name = "google-workload-identity" } }
      ] } }
    ]
    extraVolumeMounts = [
      { name = "podplane-zot-config", mountPath = "/etc/zot/config.json", subPath = "config.json", readOnly = true },
      { name = "google-identity", mountPath = "/var/run/google/identity", readOnly = true }
    ]
  }
}`)))
		values = append(values,
			field("podplane-operator", expr(`{ podplane = { operator = { googleWorkloadIdentity = { provider = google_iam_workload_identity_pool_provider.cluster.name } } } }`)),
			field("secrets-store-csi-provider-gcp", expr(`{ platform = { secretsStoreCSIProviderGCP = { googleWorkloadIdentity = { provider = google_iam_workload_identity_pool_provider.cluster.name } } } }`)),
			field("cilium", expr(`{ platform = { cilium = { blockGoogleMetadata = true } } }`)),
		)
	}
	components := object(identField("values", object(values...)))
	switch provider.Kind {
	case "aws":
		components = append(components, identField("apps", expr(`{
  "zot-registry" = { namespaceAnnotations = { "iam.amazonaws.com/allowed-roles" = jsonencode([aws_iam_role.podplane_cluster["registry-read-write"].arn]) } }
}`)))
	case "google":
		components = append(components,
			identField("crds", expr(`{ "secrets-store-csi-driver-crds" = { enabled = true } }`)),
			identField("apps", expr(`{
  "secrets-store-csi-driver" = { enabled = true }
  "secrets-store-csi-provider-gcp" = { enabled = true }
}`)),
		)
	}
	return expr("yamlencode(" + object(identField("platform", object(identField("components", components)))).renderHCL(0) + ")")
}
