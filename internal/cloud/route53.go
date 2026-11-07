// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

// CheckRoute53Zone checks the exact public zone used by generated Terraform.
// An explicit zone ID follows Terraform's ID lookup instead of name matching.
func CheckRoute53Zone(ctx context.Context, region, profile, name, id string) error {
	options := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if profile != "" {
		options = append(options, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return fmt.Errorf("load AWS credentials for Route53: %w", err)
	}
	client := route53.NewFromConfig(cfg)
	if id != "" {
		_, err := client.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(id)})
		if err != nil {
			return fmt.Errorf("check Route53 hosted zone %q: %w", id, err)
		}
		return nil
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".") + "."
	input := &route53.ListHostedZonesByNameInput{DNSName: aws.String(name)}
	matches := 0
	for {
		page, err := client.ListHostedZonesByName(ctx, input)
		if err != nil {
			return fmt.Errorf("check Route53 zone %q using the selected AWS credentials (requires route53:ListHostedZonesByName): %w", name, err)
		}
		for _, zone := range page.HostedZones {
			if strings.EqualFold(aws.ToString(zone.Name), name) && (zone.Config == nil || !zone.Config.PrivateZone) {
				matches++
			}
		}
		if !page.IsTruncated || !strings.EqualFold(aws.ToString(page.NextDNSName), name) {
			break
		}
		input.DNSName, input.HostedZoneId = page.NextDNSName, page.NextHostedZoneId
	}
	switch matches {
	case 0:
		return fmt.Errorf("no public Route53 hosted zone matches %q in the selected AWS account. A parent zone does not match. Create the zone and retry, or return to change the domain or select manual DNS", strings.TrimSuffix(name, "."))
	case 1:
		return nil
	default:
		return fmt.Errorf("multiple public Route53 hosted zones match %q; set hosted_zone_id in the cluster config to select one", strings.TrimSuffix(name, "."))
	}
}
