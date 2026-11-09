// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// EmptyBucket deletes versions, delete markers, and incomplete uploads. It never
// bypasses object retention or legal holds, and checks per-object delete errors.
func (p *Provider) EmptyBucket(ctx context.Context, bucket string) error {
	err := p.emptyBucket(ctx, bucket, "", "")
	var apiError smithy.APIError
	if errors.As(err, &apiError) && apiError.ErrorCode() == "NoSuchBucket" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("empty bucket %s: %w", bucket, err)
	}
	return nil
}

// emptyBucket removes versions and uploads, optionally preserving an owned lock version.
func (p *Provider) emptyBucket(ctx context.Context, bucket, lockKey, lockVersion string) error {
	// Always read the first page again after deleting it: deletion can invalidate
	// continuation cursors on S3-compatible services.
	for {
		page, err := p.s3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1000)})
		if err != nil {
			return err
		}
		var objects []types.ObjectIdentifier
		for _, version := range page.Versions {
			if lockKey != "" && aws.ToString(version.Key) == lockKey {
				if aws.ToString(version.VersionId) == lockVersion {
					continue
				}
				if aws.ToBool(version.IsLatest) {
					return fmt.Errorf("state lock changed; bucket retained")
				}
			}
			objects = append(objects, types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
		}
		for _, marker := range page.DeleteMarkers {
			if lockKey != "" && aws.ToString(marker.Key) == lockKey && aws.ToBool(marker.IsLatest) {
				return fmt.Errorf("state lock was removed; bucket retained")
			}
			objects = append(objects, types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if len(objects) == 0 {
			break
		}
		result, err := p.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}})
		if err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			return fmt.Errorf("delete object %s: %s: %s", aws.ToString(result.Errors[0].Key), aws.ToString(result.Errors[0].Code), aws.ToString(result.Errors[0].Message))
		}
	}
	for {
		page, err := p.s3.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket)})
		if err != nil {
			return err
		}
		if len(page.Uploads) == 0 {
			return nil
		}
		for _, upload := range page.Uploads {
			if _, err := p.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: upload.Key, UploadId: upload.UploadId}); err != nil {
				return err
			}
		}
	}
}

// DeleteBucket removes an empty bucket; Terraform owns cluster bucket deletion.
func (p *Provider) DeleteBucket(ctx context.Context, bucket string) error {
	_, err := p.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	var apiError smithy.APIError
	if errors.As(err, &apiError) && apiError.ErrorCode() == "NoSuchBucket" {
		return nil
	}
	return err
}
