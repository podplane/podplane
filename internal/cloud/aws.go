// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"fmt"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// AWSAccount resolves the account using the same region and credential profile
// as the AWS infrastructure and remote-state clients.
func AWSAccount(ctx context.Context, region, profile string) (string, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return "", fmt.Errorf("load AWS credentials: %w", err)
	}
	identity, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("resolve AWS account: %w", err)
	}
	account := aws.ToString(identity.Account)
	if err := ValidateAWSAccount(account); err != nil {
		return "", err
	}
	return account, nil
}

// ValidateAWSAccount requires a twelve-digit AWS account ID.
func ValidateAWSAccount(value string) error {
	if matched, _ := regexp.MatchString(`^[0-9]{12}$`, value); !matched {
		return fmt.Errorf("AWS account ID must contain exactly 12 digits")
	}
	return nil
}
