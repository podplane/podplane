// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/podplane/podplane/internal/clusterdelete"
)

// stateLock holds the conditional-write identity of an S3 backend lock.
type stateLock struct {
	provider                   *Provider
	bucket, key, etag, version string
	emptied                    bool
}

// LockState acquires the same conditional lock object as the S3 engine backend.
// A conflicting lock or unsupported conditional write stops teardown.
func (p *Provider) LockState(ctx context.Context, bucket, key string) (clusterdelete.StateLock, error) {
	body, err := clusterdelete.StateLockInfo(bucket + "/" + key)
	if err != nil {
		return nil, err
	}
	result, err := p.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*"),
	}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return nil, fmt.Errorf("acquire state lock s3://%s/%s (inspect the lock before retrying): %w", bucket, key, err)
	}
	version := aws.ToString(result.VersionId)
	if version == "" {
		version = "null"
	}
	if aws.ToString(result.ETag) == "" {
		return nil, fmt.Errorf("S3 returned no state lock ETag; lock retained at s3://%s/%s", bucket, key)
	}
	return &stateLock{provider: p, bucket: bucket, key: key, etag: *result.ETag, version: version}, nil
}

// EmptyBucket removes all versions and uploads except the live owned lock.
func (l *stateLock) EmptyBucket(ctx context.Context) error {
	if err := l.provider.emptyBucket(ctx, l.bucket, l.key, l.version); err != nil {
		return err
	}
	l.emptied = true
	return nil
}

// Unlock conditionally removes our lock, never a replacement client's lock.
func (l *stateLock) Unlock(ctx context.Context) error {
	input := &s3.DeleteObjectInput{Bucket: aws.String(l.bucket), Key: aws.String(l.key), IfMatch: aws.String(l.etag)}
	if l.emptied {
		// Remove the version itself without leaving a delete marker. Older lock
		// versions have already been removed, so none can become live again.
		input.VersionId = aws.String(l.version)
	}
	if _, err := l.provider.s3.DeleteObject(ctx, input, func(o *s3.Options) { o.RetryMaxAttempts = 1 }); err != nil {
		return fmt.Errorf("release state lock s3://%s/%s: %w", l.bucket, l.key, err)
	}
	return nil
}
