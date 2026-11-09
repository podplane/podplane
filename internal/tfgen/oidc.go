// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/podplane/podplane/internal/oidcconfig"
)

// TrusterModuleVersion is the Truster release whose OpenTofu/Terraform
// module Podplane deploys. schemas/podplane.oidc.truster.schema.json is a
// copy of that release's configuration schema; update it with
// make truster-schema whenever this changes.
const TrusterModuleVersion = "3.1.0"

// GenerateOIDC renders managed Terraform files for an OIDC config on AWS or
// Google Cloud.
func GenerateOIDC(cfg *oidcconfig.Config) ([]File, error) {
	if err := oidcconfig.Validate(cfg); err != nil {
		return nil, err
	}
	var mainDoc, variablesDoc, outputsDoc hclDocument
	mainDoc.header = []string{
		"OIDC server infrastructure, networking, and optional managed DNS records.",
	}
	variablesDoc.header = []string{
		"OIDC input variables. Settings currently come directly from podplane.oidc.jsonc.",
	}
	outputsDoc.header = []string{
		"OIDC issuer URL for configuring cluster authentication.",
	}

	o := cfg.OIDC
	terraform := block("terraform")
	terraform.Body.Attr("required_version", str(">= 1.10.0"))
	requiredProviders := block("required_providers")
	requiredProviders.Body.Attr(o.Provider.Kind, object(
		identField("source", str("hashicorp/"+o.Provider.Kind)),
		identField("version", str(">= 6.0")),
	))
	terraform.Body.Block(requiredProviders)
	switch o.Provider.Kind {
	case "aws":
		terraform.Body.Block(block("backend", "s3"))
		mainDoc.AddBlock(terraform)
		addAWSOIDC(&mainDoc, o)
	case "google":
		terraform.Body.Block(block("backend", "gcs"))
		mainDoc.AddBlock(terraform)
		addGoogleOIDC(&mainDoc, o)
	default:
		return nil, fmt.Errorf("OIDC provider %q is not supported", o.Provider.Kind)
	}

	output := block("output", "oidc_issuer_url")
	output.Body.Attr("value", expr("module.oidc.issuer_url"))
	outputsDoc.AddBlock(output)
	return []File{
		{Name: "podplane.oidc.main.tf", Content: mainDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.oidc.variables.tf", Content: variablesDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.oidc.outputs.tf", Content: outputsDoc.String(), Type: FileTypeTerraform},
	}, nil
}

// WriteOIDC writes managed Terraform files for an OIDC config.
func WriteOIDC(configPath string, cfg *oidcconfig.Config) error {
	files, err := GenerateOIDC(cfg)
	if err != nil {
		return err
	}
	if err := WriteFiles(filepath.Dir(configPath), files); err != nil {
		return err
	}
	return oidcconfig.WriteSchema(configPath)
}

// addAWSOIDC adds the AWS provider, VPC, Truster module, and optional Route 53
// records to doc.
func addAWSOIDC(doc *hclDocument, o oidcconfig.OIDC) {
	name := oidcNamePrefix(o.Hostname)
	provider := block("provider", "aws")
	provider.Body.Attr("region", str(o.Provider.Region))
	if o.Provider.Profile != "" {
		provider.Body.Attr("profile", str(o.Provider.Profile))
	}
	doc.AddBlock(provider)

	if o.Domain.Provider.Kind == "aws" && o.Domain.Zone != "" {
		zone := block("data", "aws_route53_zone", "oidc")
		if o.Domain.Provider.HostedZoneID != "" {
			zone.Body.Attr("zone_id", str(o.Domain.Provider.HostedZoneID))
		} else {
			zoneName := o.Domain.Zone
			if !strings.HasSuffix(zoneName, ".") {
				zoneName += "."
			}
			zone.Body.Attr("name", str(zoneName))
		}
		doc.AddBlock(zone)
	}

	vpc := block("resource", "aws_vpc", "oidc")
	vpc.Body.Attr("cidr_block", str("10.0.0.0/16"))
	vpc.Body.Attr("assign_generated_ipv6_cidr_block", boolean(true))
	vpc.Body.Attr("enable_dns_hostnames", boolean(true))
	vpc.Body.Attr("enable_dns_support", boolean(true))
	vpc.Body.Attr("tags", object(identField("Name", str(name+"-vpc"))))
	doc.AddBlock(vpc)

	internetGateway := block("resource", "aws_internet_gateway", "oidc")
	internetGateway.Body.Attr("vpc_id", expr("aws_vpc.oidc.id"))
	internetGateway.Body.Attr("tags", object(identField("Name", str(name+"-igw"))))
	doc.AddBlock(internetGateway)

	routeTable := block("resource", "aws_route_table", "oidc")
	routeTable.Body.Attr("vpc_id", expr("aws_vpc.oidc.id"))
	defaultIPv4Route := block("route")
	defaultIPv4Route.Body.Attr("cidr_block", str("0.0.0.0/0"))
	defaultIPv4Route.Body.Attr("gateway_id", expr("aws_internet_gateway.oidc.id"))
	routeTable.Body.Block(defaultIPv4Route)
	defaultIPv6Route := block("route")
	defaultIPv6Route.Body.Attr("ipv6_cidr_block", str("::/0"))
	defaultIPv6Route.Body.Attr("gateway_id", expr("aws_internet_gateway.oidc.id"))
	routeTable.Body.Block(defaultIPv6Route)
	routeTable.Body.Attr("tags", object(identField("Name", str(name+"-rt"))))
	doc.AddBlock(routeTable)

	module := block("module", "oidc")
	module.Body.Attr("source", str("truster-dev/truster/aws"))
	module.Body.Attr("version", str(TrusterModuleVersion))
	module.Body.Attr("name_prefix", str(name))
	module.Body.Attr("vpc_id", expr("aws_vpc.oidc.id"))
	module.Body.Attr("oidc_addr", str(hostOnly(o.Hostname)))
	module.Body.Attr("secrets_provider", str("aws-secrets-manager"))
	module.Body.Attr("truster_config", trusterValue(o.Truster))
	doc.AddBlock(module)

	routeTableAssociation := block("resource", "aws_route_table_association", "oidc")
	routeTableAssociation.Body.Attr("subnet_id", expr("module.oidc.subnet_id"))
	routeTableAssociation.Body.Attr("route_table_id", expr("aws_route_table.oidc.id"))
	doc.AddBlock(routeTableAssociation)

	if o.Domain.Provider.Kind == "aws" && o.Domain.Zone != "" {
		hostname := hostOnly(o.Hostname)
		ipv4Record := block("resource", "aws_route53_record", "oidc_ipv4")
		ipv4Record.Body.Attr("count", expr("module.oidc.enable_ipv4 ? 1 : 0"))
		ipv4Record.Body.Attr("zone_id", expr("data.aws_route53_zone.oidc.zone_id"))
		ipv4Record.Body.Attr("name", str(hostname))
		ipv4Record.Body.Attr("type", str("A"))
		ipv4Record.Body.Attr("ttl", num(300))
		ipv4Record.Body.Attr("records", list(expr("module.oidc.public_ipv4")))
		doc.AddBlock(ipv4Record)

		ipv6Record := block("resource", "aws_route53_record", "oidc_ipv6")
		ipv6Record.Body.Attr("count", expr("module.oidc.enable_ipv6 ? 1 : 0"))
		ipv6Record.Body.Attr("zone_id", expr("data.aws_route53_zone.oidc.zone_id"))
		ipv6Record.Body.Attr("name", str(hostname))
		ipv6Record.Body.Attr("type", str("AAAA"))
		ipv6Record.Body.Attr("ttl", num(300))
		ipv6Record.Body.Attr("records", list(expr("module.oidc.public_ipv6")))
		doc.AddBlock(ipv6Record)
	}
}

// addGoogleOIDC adds the Google provider, VPC network, Truster module, and
// optional Cloud DNS records to doc.
func addGoogleOIDC(doc *hclDocument, o oidcconfig.OIDC) {
	name := oidcNamePrefix(o.Hostname)
	provider := block("provider", "google")
	provider.Body.Attr("project", str(o.Provider.Project))
	provider.Body.Attr("region", str(o.Provider.Region))
	provider.Body.Attr("zone", str(o.Provider.Zone))
	doc.AddBlock(provider)

	network := block("resource", "google_compute_network", "oidc")
	network.Body.Attr("name", str(name))
	network.Body.Attr("auto_create_subnetworks", boolean(false))
	doc.AddBlock(network)

	module := block("module", "oidc")
	module.Body.Attr("source", str("truster-dev/truster/google"))
	module.Body.Attr("version", str(TrusterModuleVersion))
	module.Body.Attr("name_prefix", str(name))
	module.Body.Attr("network", expr("google_compute_network.oidc.self_link"))
	module.Body.Attr("oidc_addr", str(hostOnly(o.Hostname)))
	module.Body.Attr("truster_config", trusterValue(o.Truster))
	doc.AddBlock(module)

	if o.Domain.Provider.Kind == "google" && o.Domain.Zone != "" {
		for _, record := range []struct{ name, kind, enabled, address string }{
			{"oidc_ipv4", "A", "enable_ipv4", "public_ipv4"},
			{"oidc_ipv6", "AAAA", "enable_ipv6", "public_ipv6"},
		} {
			recordSet := block("resource", "google_dns_record_set", record.name)
			recordSet.Body.Attr("count", expr("module.oidc."+record.enabled+" ? 1 : 0"))
			recordSet.Body.Attr("managed_zone", str(o.Domain.Provider.HostedZoneName))
			recordSet.Body.Attr("name", str(strings.TrimSuffix(hostOnly(o.Hostname), ".")+"."))
			recordSet.Body.Attr("type", str(record.kind))
			recordSet.Body.Attr("ttl", num(300))
			recordSet.Body.Attr("rrdatas", list(expr("module.oidc."+record.address)))
			doc.AddBlock(recordSet)
		}
	}
}

// oidcNamePrefix identifies a hostname consistently across cloud providers.
// Google's service-account ID imposes the strictest limit: start with a letter
// and fit in 30 characters. Keep the hash last, including the full hostname in
// it so truncation and replacing dots with hyphens cannot alias deployments.
func oidcNamePrefix(hostname string) string {
	hostname = strings.TrimSuffix(strings.ToLower(hostOnly(hostname)), ".")
	hash := sha256.Sum256([]byte(hostname))
	slug := strings.ReplaceAll(hostname, ".", "-")
	if len(slug) > 8 {
		slug = slug[:8]
	}
	return fmt.Sprintf("oidc-%s-%x", strings.TrimRight(slug, "-"), hash[:8])
}

// trusterValue converts a Truster configuration decoded from JSON into an
// HCL value, keeping strings literal by escaping template sequences.
func trusterValue(value any) hclValue {
	switch v := value.(type) {
	case map[string]any:
		fields := make([]hclObjectField, 0, len(v))
		for _, key := range sortedKeys(v) {
			fields = append(fields, field(key, trusterValue(v[key])))
		}
		return object(fields...)
	case []any:
		items := make(hclList, 0, len(v))
		for _, item := range v {
			items = append(items, trusterValue(item))
		}
		return items
	case string:
		return str(strings.NewReplacer("${", "$${", "%{", "%%{").Replace(v))
	case float64:
		return expr(strconv.FormatFloat(v, 'f', -1, 64))
	case bool:
		return boolean(v)
	default:
		return expr("null")
	}
}

// hostOnly returns the host component from a hostname or URL.
func hostOnly(hostname string) string {
	if !strings.Contains(hostname, "://") {
		return hostname
	}
	u, err := url.Parse(hostname)
	if err != nil || u.Host == "" {
		return hostname
	}
	return u.Host
}
