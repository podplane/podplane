// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"errors"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type caCertificateStore interface {
	Read(context.Context) ([]byte, error)
	Create(context.Context, []byte) error
	Close() error
}

func newCACertificateStore(ctx context.Context, m nstanceCAModel) (caCertificateStore, error) {
	opts := SeedOptions{Bucket: m.Bucket.ValueString(), Region: m.Region.ValueString(), Profile: m.Profile.ValueString(), Project: m.Project.ValueString()}
	if m.Provider.ValueString() == cloudSecretProviderGCPSecretManager {
		client, err := newGCSClient(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &gcsCACertificateStore{client: client, bucket: opts.Bucket}, nil
	}
	client, err := newS3Client(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &s3CACertificateStore{client: client, bucket: opts.Bucket}, nil
}

type s3CACertificateStore struct {
	client *s3.Client
	bucket string
}

func (s *s3CACertificateStore) Read(ctx context.Context) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String("cluster/ca.crt")})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NoSuchKey" {
			return nil, errSnapshotAbsent
		}
		return nil, errors.New("cannot read Nstance CA certificate from S3")
	}
	defer func() { _ = out.Body.Close() }()
	return readPublicCA(out.Body)
}

func (s *s3CACertificateStore) Create(ctx context.Context, cert []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String("cluster/ca.crt"), Body: bytes.NewReader(cert),
		IfNoneMatch: aws.String("*"), ServerSideEncryption: types.ServerSideEncryptionAes256,
	})
	if err != nil {
		return errors.New("create-only CA certificate publication failed in S3")
	}
	return nil
}

func (*s3CACertificateStore) Close() error { return nil }

type gcsCACertificateStore struct {
	client *storage.Client
	bucket string
}

func (s *gcsCACertificateStore) Read(ctx context.Context) ([]byte, error) {
	r, err := s.client.Bucket(s.bucket).Object("cluster/ca.crt").NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, errSnapshotAbsent
	}
	if err != nil {
		return nil, errors.New("cannot read Nstance CA certificate from GCS")
	}
	defer func() { _ = r.Close() }()
	return readPublicCA(r)
}

func (s *gcsCACertificateStore) Create(ctx context.Context, cert []byte) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := s.client.Bucket(s.bucket).Object("cluster/ca.crt").If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	if _, err := w.Write(cert); err != nil {
		cancel()
		_ = w.Close()
		return errors.New("create-only CA certificate publication failed in GCS")
	}
	if err := w.Close(); err != nil {
		return errors.New("create-only CA certificate publication failed in GCS")
	}
	return nil
}

func (s *gcsCACertificateStore) Close() error { return s.client.Close() }
