// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// s3SeedStore manages create-only Netsy snapshots and public CA reads in S3.
type s3SeedStore struct {
	client *s3.Client
	opts   SeedOptions
}

// Metadata reads the snapshot version, completion marker, and encryption status.
func (s *s3SeedStore) Metadata(ctx context.Context) (seedSnapshotMetadata, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.opts.Bucket), Key: aws.String(s.opts.objectKey())})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NotFound" || api.ErrorCode() == "NoSuchKey") {
			return seedSnapshotMetadata{}, errSnapshotAbsent
		}
		return seedSnapshotMetadata{}, errors.New("cannot read S3 seed metadata")
	}
	version := aws.ToString(out.VersionId)
	if version == "" {
		version = aws.ToString(out.ETag)
	}
	return seedSnapshotMetadata{Version: version, Completion: out.Metadata[seedCompletionMetadata], Encrypted: out.ServerSideEncryption != ""}, nil
}

// RequireEmpty rejects a seed prefix that already contains objects.
func (s *s3SeedStore) RequireEmpty(ctx context.Context) error {
	return requireEmptyS3Prefix(ctx, s.client, s.opts.Bucket, s.opts.objectPrefix())
}

// PublicCA downloads Nstance's public CA certificate with a bounded read.
func (s *s3SeedStore) PublicCA(ctx context.Context, b *nstanceBootstrapModel) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.CABucket.ValueString()), Key: aws.String(path.Join(strings.Trim(b.CAPrefix.ValueString(), "/"), "ca.crt"))})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NoSuchKey" {
			return nil, errSnapshotAbsent
		}
		return nil, errors.New("cannot read Nstance public CA from S3")
	}
	defer func() { _ = out.Body.Close() }()
	return readPublicCA(out.Body)
}

// Upload creates an encrypted snapshot and completion marker without overwriting an object.
func (s *s3SeedStore) Upload(ctx context.Context, snapshot []byte, completion string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.opts.Bucket), Key: aws.String(s.opts.objectKey()), Body: bytes.NewReader(snapshot),
		IfNoneMatch: aws.String("*"), ServerSideEncryption: types.ServerSideEncryptionAes256,
		Metadata: map[string]string{seedCompletionMetadata: completion},
	})
	// Do not propagate payload-bearing backend errors into diagnostics.
	if err != nil {
		return errors.New("S3 create-only seed upload failed")
	}
	return nil
}

// newS3Client returns an S3 client using the standard AWS SDK credential chain.
func newS3Client(ctx context.Context, opts SeedOptions) (*s3.Client, error) {
	loadOpts := []func(*config.LoadOptions) error{}
	if opts.Region != "" {
		loadOpts = append(loadOpts, config.WithRegion(opts.Region))
	}
	if opts.Profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(opts.Profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// requireEmptyS3Prefix fails when any object already exists under prefix.
func requireEmptyS3Prefix(ctx context.Context, client *s3.Client, bucket, prefix string) error {
	in := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		MaxKeys: aws.Int32(1),
	}
	if prefix != "" {
		in.Prefix = aws.String(prefix + "/")
	}
	out, err := client.ListObjectsV2(ctx, in)
	if err != nil {
		return fmt.Errorf("check S3 prefix s3://%s/%s is empty: %w", bucket, prefix, err)
	}
	if out.KeyCount != nil && *out.KeyCount > 0 || len(out.Contents) > 0 {
		return fmt.Errorf("S3 prefix s3://%s/%s already contains Netsy state or other objects", bucket, prefix)
	}
	return nil
}
