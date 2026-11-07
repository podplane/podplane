// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfstate

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// remoteStateRecoveryDays is how long new buckets keep noncurrent state
// versions before their lifecycle rule deletes them.
const remoteStateRecoveryDays int32 = 90

// s3Client returns an S3 client for the backend region, using the backend
// profile when set.
func (b Backend) s3Client(ctx context.Context) (*s3.Client, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(b.Region)}
	if b.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(b.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration for remote state: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// stateTagKey is the bucket tag recording Podplane's progress configuring a
// bucket it created: stateTagPending from creation until every setting is in
// place, then stateTagConfigured. Buckets without it were created by someone
// else, and Podplane only verifies them.
const (
	stateTagKey        = "podplane:tfstate"
	stateTagPending    = "pending"
	stateTagConfigured = "configured"
)

// checkS3 verifies an existing S3 state bucket owned by the backend account.
// It reports errBucketAbsent when the bucket does not exist, and
// errBucketIncomplete when Podplane created it but did not finish
// configuring it.
func (b Backend) checkS3(ctx context.Context, warn io.Writer) error {
	client, err := b.s3Client(ctx)
	if err != nil {
		return err
	}
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	if s3ErrorCode(err) == "NotFound" {
		return fmt.Errorf("remote-state bucket s3://%s %w", b.Bucket, errBucketAbsent)
	}
	if err != nil {
		return fmt.Errorf("check remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	tags, err := b.tagsS3(ctx, client)
	if err != nil {
		return err
	}
	markers := make(map[string]string, len(tags))
	for _, tag := range tags {
		markers[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	if err := b.verifyIdentity(markers); err != nil {
		return err
	}
	if tagValue(tags, stateTagKey) == stateTagPending {
		return fmt.Errorf("remote-state bucket s3://%s %w", b.Bucket, errBucketIncomplete)
	}
	return b.verifyS3(ctx, client, warn)
}

// createS3 creates the S3 state bucket tagged as pending, then configures it.
// A bucket another process created concurrently is verified, or configured
// if it is still pending.
func (b Backend) createS3(ctx context.Context) error {
	client, err := b.s3Client(ctx)
	if err != nil {
		return err
	}
	configuration := &types.CreateBucketConfiguration{Tags: []types.Tag{{Key: aws.String(stateTagKey), Value: aws.String(stateTagPending)}}}
	for key, value := range b.identityMarkers() {
		configuration.Tags = append(configuration.Tags, types.Tag{Key: aws.String(key), Value: aws.String(value)})
	}
	if b.Region != "us-east-1" {
		configuration.LocationConstraint = types.BucketLocationConstraint(b.Region)
	}
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b.Bucket), CreateBucketConfiguration: configuration}); err != nil {
		checkErr := b.checkS3(ctx, io.Discard)
		if checkErr == nil {
			return nil
		}
		if !errors.Is(checkErr, errBucketIncomplete) {
			return fmt.Errorf("create remote-state bucket s3://%s: %w", b.Bucket, err)
		}
	}
	return b.configureS3(ctx)
}

// configureS3 applies Podplane's settings to a bucket it created and then
// marks the bucket configured. Every step is idempotent, so a failed run is
// finished by rerunning it.
func (b Backend) configureS3(ctx context.Context) error {
	client, err := b.s3Client(ctx)
	if err != nil {
		return err
	}
	tags, err := b.tagsS3(ctx, client)
	if err != nil {
		return err
	}
	markers := make(map[string]string, len(tags))
	for _, tag := range tags {
		markers[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	if err := b.verifyIdentity(markers); err != nil {
		return err
	}
	if err := b.hardenS3(ctx, client); err != nil {
		return fmt.Errorf("%w; the bucket is only partly configured, and rerunning will finish configuring it", err)
	}
	tagSet := []types.Tag{{Key: aws.String(stateTagKey), Value: aws.String(stateTagConfigured)}}
	for _, tag := range tags {
		if aws.ToString(tag.Key) != stateTagKey {
			tagSet = append(tagSet, tag)
		}
	}
	if _, err := client.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account), Tagging: &types.Tagging{TagSet: tagSet}}); err != nil {
		return fmt.Errorf("mark remote-state bucket s3://%s configured: %w; rerunning will finish configuring it", b.Bucket, err)
	}
	return nil
}

// tagsS3 returns the bucket's tags, which are empty when it has none.
func (b Backend) tagsS3(ctx context.Context, client *s3.Client) ([]types.Tag, error) {
	tagging, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	if s3ErrorCode(err) == "NoSuchTagSet" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tags for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	return tagging.TagSet, nil
}

// tagValue returns the value of the tag with key, or "" when it is absent.
func tagValue(tags []types.Tag, key string) string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == key {
			return aws.ToString(tag.Value)
		}
	}
	return ""
}

// verifyS3 requires an existing S3 bucket to be private and encrypted by
// default, and warns when versioning is disabled.
func (b Backend) verifyS3(ctx context.Context, client *s3.Client, warn io.Writer) error {
	versioning, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	if err != nil {
		return fmt.Errorf("read versioning for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	if versioning.Status != types.BucketVersioningStatusEnabled {
		_, _ = fmt.Fprintf(warn, "Warning: remote-state bucket s3://%s does not have versioning enabled; versioning is recommended for state recovery\n", b.Bucket)
	}
	private, err := b.privateS3(ctx, client)
	if err != nil {
		return err
	}
	if !private {
		return fmt.Errorf("existing remote-state bucket s3://%s must block public access, or disable ACLs and have no public bucket policy", b.Bucket)
	}
	encryption, err := client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	if err != nil {
		return fmt.Errorf("read encryption for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	if encryption.ServerSideEncryptionConfiguration == nil || len(encryption.ServerSideEncryptionConfiguration.Rules) == 0 {
		return fmt.Errorf("existing remote-state bucket s3://%s must enable default encryption", b.Bucket)
	}
	return nil
}

// privateS3 reports whether the bucket cannot be made public. That holds when
// the bucket blocks all public access, or when its ACLs are disabled and its
// bucket policy is not public, as with buckets protected by an account-level
// public access block.
func (b Backend) privateS3(ctx context.Context, client *s3.Client) (bool, error) {
	block, err := client.GetPublicAccessBlock(ctx, &s3.GetPublicAccessBlockInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	switch {
	case err == nil:
		c := block.PublicAccessBlockConfiguration
		if c != nil && aws.ToBool(c.BlockPublicAcls) && aws.ToBool(c.BlockPublicPolicy) && aws.ToBool(c.IgnorePublicAcls) && aws.ToBool(c.RestrictPublicBuckets) {
			return true, nil
		}
	case s3ErrorCode(err) != "NoSuchPublicAccessBlockConfiguration":
		return false, fmt.Errorf("read public access block for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	ownership, err := client.GetBucketOwnershipControls(ctx, &s3.GetBucketOwnershipControlsInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	if s3ErrorCode(err) == "OwnershipControlsNotFoundError" {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read object ownership for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	if ownership.OwnershipControls == nil || len(ownership.OwnershipControls.Rules) == 0 || ownership.OwnershipControls.Rules[0].ObjectOwnership != types.ObjectOwnershipBucketOwnerEnforced {
		return false, nil
	}
	status, err := client.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: aws.String(b.Account)})
	if s3ErrorCode(err) == "NoSuchBucketPolicy" {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read policy status for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	return status.PolicyStatus == nil || !aws.ToBool(status.PolicyStatus.IsPublic), nil
}

// hardenS3 enables versioning, default encryption, public-access blocking,
// and the noncurrent-version recovery lifecycle on a new S3 bucket.
func (b Backend) hardenS3(ctx context.Context, client *s3.Client) error {
	owner := aws.String(b.Account)
	if _, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: owner, VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		return fmt.Errorf("enable versioning on remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	if _, err := client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: owner, ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{Rules: []types.ServerSideEncryptionRule{{ApplyServerSideEncryptionByDefault: &types.ServerSideEncryptionByDefault{SSEAlgorithm: types.ServerSideEncryptionAes256}}}}}); err != nil {
		return fmt.Errorf("enable encryption on remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	_, err := client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{Bucket: aws.String(b.Bucket), ExpectedBucketOwner: owner, PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{BlockPublicAcls: aws.Bool(true), BlockPublicPolicy: aws.Bool(true), IgnorePublicAcls: aws.Bool(true), RestrictPublicBuckets: aws.Bool(true)}})
	if err != nil {
		return fmt.Errorf("block public access on remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	_, err = client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket:              aws.String(b.Bucket),
		ExpectedBucketOwner: owner,
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: []types.LifecycleRule{{
			Filter:                      &types.LifecycleRuleFilter{Prefix: aws.String("podplane/")},
			ID:                          aws.String("podplane-remote-state-recovery"),
			NoncurrentVersionExpiration: &types.NoncurrentVersionExpiration{NoncurrentDays: aws.Int32(remoteStateRecoveryDays)},
			Status:                      types.ExpirationStatusEnabled,
		}}},
	})
	if err != nil {
		return fmt.Errorf("configure recovery lifecycle for remote-state bucket s3://%s: %w", b.Bucket, err)
	}
	return nil
}

// s3ErrorCode returns the S3 API error code in err, or "" when err is nil or
// not an S3 API error.
func s3ErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}
