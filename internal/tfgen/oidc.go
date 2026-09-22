// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/podplane/podplane/internal/oidcconfig"
)

// GenerateOIDC renders managed Terraform files for an OIDC config.
func GenerateOIDC(cfg *oidcconfig.Config) ([]File, error) {
	if err := oidcconfig.Validate(cfg); err != nil {
		return nil, err
	}
	if cfg.OIDC.Provider.Kind != "aws" {
		return nil, fmt.Errorf("OIDC provider %q is not supported", cfg.OIDC.Provider.Kind)
	}
	return renderAWSOIDC(cfg), nil
}

// WriteOIDC writes managed Terraform files for an OIDC config.
func WriteOIDC(dir string, cfg *oidcconfig.Config) error {
	files, err := GenerateOIDC(cfg)
	if err != nil {
		return err
	}
	if err := WriteFiles(dir, files); err != nil {
		return err
	}
	return oidcconfig.WriteSchema(dir)
}

// renderAWSOIDC renders the AWS Truster Terraform files.
func renderAWSOIDC(cfg *oidcconfig.Config) []File {
	o := cfg.OIDC
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
	terraform.Body.Block(requiredProviders)
	mainDoc.AddBlock(terraform)

	provider := block("provider", "aws")
	provider.Body.Attr("region", str(o.Provider.Region))
	if o.Provider.Profile != "" {
		provider.Body.Attr("profile", str(o.Provider.Profile))
	}
	mainDoc.AddBlock(provider)

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
		mainDoc.AddBlock(zone)
	}

	vpc := block("resource", "aws_vpc", "oidc")
	vpc.Body.Attr("cidr_block", str("10.0.0.0/16"))
	vpc.Body.Attr("assign_generated_ipv6_cidr_block", boolean(true))
	vpc.Body.Attr("enable_dns_hostnames", boolean(true))
	vpc.Body.Attr("enable_dns_support", boolean(true))
	vpc.Body.Attr("tags", object(identField("Name", str("truster-vpc"))))
	mainDoc.AddBlock(vpc)

	internetGateway := block("resource", "aws_internet_gateway", "oidc")
	internetGateway.Body.Attr("vpc_id", expr("aws_vpc.oidc.id"))
	internetGateway.Body.Attr("tags", object(identField("Name", str("truster-igw"))))
	mainDoc.AddBlock(internetGateway)

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
	routeTable.Body.Attr("tags", object(identField("Name", str("truster-rt"))))
	mainDoc.AddBlock(routeTable)

	module := block("module", "oidc")
	module.Body.Attr("source", str("truster/truster/aws"))
	module.Body.Attr("vpc_id", expr("aws_vpc.oidc.id"))
	module.Body.Attr("oidc_addr", str(hostOnly(o.Hostname)))
	module.Body.Attr("secrets_provider", str("aws-secrets-manager"))
	module.Body.Attr("truster_config", trusterConfigValue(o))
	mainDoc.AddBlock(module)

	routeTableAssociation := block("resource", "aws_route_table_association", "oidc")
	routeTableAssociation.Body.Attr("subnet_id", expr("module.oidc.subnet_id"))
	routeTableAssociation.Body.Attr("route_table_id", expr("aws_route_table.oidc.id"))
	mainDoc.AddBlock(routeTableAssociation)

	if o.Domain.Provider.Kind == "aws" && o.Domain.Zone != "" {
		hostname := hostOnly(o.Hostname)
		ipv4Record := block("resource", "aws_route53_record", "oidc_ipv4")
		ipv4Record.Body.Attr("count", expr("module.oidc.enable_ipv4 ? 1 : 0"))
		ipv4Record.Body.Attr("zone_id", expr("data.aws_route53_zone.oidc.zone_id"))
		ipv4Record.Body.Attr("name", str(hostname))
		ipv4Record.Body.Attr("type", str("A"))
		ipv4Record.Body.Attr("ttl", num(300))
		ipv4Record.Body.Attr("records", list(expr("module.oidc.public_ipv4")))
		mainDoc.AddBlock(ipv4Record)

		ipv6Record := block("resource", "aws_route53_record", "oidc_ipv6")
		ipv6Record.Body.Attr("count", expr("module.oidc.enable_ipv6 ? 1 : 0"))
		ipv6Record.Body.Attr("zone_id", expr("data.aws_route53_zone.oidc.zone_id"))
		ipv6Record.Body.Attr("name", str(hostname))
		ipv6Record.Body.Attr("type", str("AAAA"))
		ipv6Record.Body.Attr("ttl", num(300))
		ipv6Record.Body.Attr("records", list(expr("module.oidc.public_ipv6")))
		mainDoc.AddBlock(ipv6Record)
	}

	output := block("output", "oidc_issuer_url")
	output.Body.Attr("value", expr("module.oidc.issuer_url"))
	outputsDoc.AddBlock(output)
	return []File{
		{Name: "podplane.oidc.main.tf", Content: mainDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.oidc.variables.tf", Content: variablesDoc.String(), Type: FileTypeTerraform},
		{Name: "podplane.oidc.outputs.tf", Content: outputsDoc.String(), Type: FileTypeTerraform},
	}
}

// trusterConfigValue converts Podplane's OIDC settings to Truster v2 config.
func trusterConfigValue(config oidcconfig.OIDC) hclObject {
	secretFields := []hclObjectField{
		identField("signing_key_name", str(config.SigningKeySecretARN)),
	}
	if config.EncryptionKeySecretARN != "" {
		secretFields = append(secretFields, identField("encryption_key_name", str(config.EncryptionKeySecretARN)))
	}

	displayName := "Google"
	if config.Connector.Kind == "github" {
		displayName = "GitHub"
	}
	connector := object(
		identField("type", str(config.Connector.Kind)),
		identField("display_name", str(displayName)),
		identField("credentials_secret", str(config.Connector.ClientSecretARN)),
	)

	policyFields := []hclObjectField{
		identField("clients", oidcClientsValue(config.Clients)),
	}
	if len(config.DefaultRedirectURIs) > 0 {
		policyFields = append([]hclObjectField{
			identField("default_redirect_uris", stringValueList(config.DefaultRedirectURIs)),
		}, policyFields...)
	}
	if len(config.GroupsOverrides) > 0 {
		policyFields = append(policyFields, identField("user_group_mappings", groupsOverridesValue(config.GroupsOverrides)))
	}

	return object(
		identField("secrets", object(secretFields...)),
		identField("user_login_connectors", object(field(config.Connector.Kind, connector))),
		identField("static_policy", object(policyFields...)),
	)
}

// oidcClientsValue converts configured clients into a Truster static-policy
// object.
func oidcClientsValue(clients map[string]oidcconfig.Client) hclObject {
	fields := make([]hclObjectField, 0, len(clients))
	for _, name := range sortedKeys(clients) {
		client := clients[name]
		clientFields := []hclObjectField{}
		if client.GroupsOverride != "" {
			clientFields = append(clientFields, identField("user_group_mapping", str(client.GroupsOverride)))
		}
		if len(client.RedirectURIs) > 0 {
			clientFields = append(clientFields, identField("redirect_uris", stringValueList(client.RedirectURIs)))
		}
		fields = append(fields, field(name, object(clientFields...)))
	}
	return object(fields...)
}

// groupsOverridesValue converts group overrides into Truster user-group
// mappings.
func groupsOverridesValue(groups map[string]oidcconfig.GroupsOverride) hclObject {
	fields := make([]hclObjectField, 0, len(groups))
	for _, name := range sortedKeys(groups) {
		override := groups[name]
		overrideFields := make([]hclObjectField, 0, len(override))
		for _, email := range sortedKeys(override) {
			overrideFields = append(overrideFields, field(email, stringValueList(override[email])))
		}
		fields = append(fields, field(name, object(overrideFields...)))
	}
	return object(fields...)
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
