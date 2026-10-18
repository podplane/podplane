// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// gcsSeedStore manages create-only Netsy snapshots and public CA reads in GCS.
type gcsSeedStore struct {
	client *storage.Client
	opts   SeedOptions
}

// Metadata reads the snapshot generation and completion marker without its payload.
func (s *gcsSeedStore) Metadata(ctx context.Context) (seedSnapshotMetadata, error) {
	attrs, err := s.client.Bucket(s.opts.Bucket).Object(s.opts.objectKey()).Attrs(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return seedSnapshotMetadata{}, errSnapshotAbsent
	}
	if err != nil {
		return seedSnapshotMetadata{}, errors.New("cannot read GCS seed metadata")
	}
	// All GCS objects are encrypted at rest, including provider-managed keys.
	return seedSnapshotMetadata{Version: strconv.FormatInt(attrs.Generation, 10), Completion: attrs.Metadata[seedCompletionMetadata], Encrypted: true}, nil
}

// RequireEmpty rejects a seed prefix that already contains objects.
func (s *gcsSeedStore) RequireEmpty(ctx context.Context) error {
	return requireEmptyGCSPrefix(ctx, s.client, s.opts.Bucket, s.opts.objectPrefix())
}

// PublicCA downloads Nstance's public CA certificate with a bounded read.
func (s *gcsSeedStore) PublicCA(ctx context.Context, b *nstanceBootstrapModel) ([]byte, error) {
	r, err := s.client.Bucket(b.CABucket.ValueString()).Object(path.Join(strings.Trim(b.CAPrefix.ValueString(), "/"), "ca.crt")).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, errSnapshotAbsent
	}
	if err != nil {
		return nil, errors.New("cannot read Nstance public CA from GCS")
	}
	defer func() { _ = r.Close() }()
	return readPublicCA(r)
}

// Upload creates the snapshot and completion marker without overwriting an object.
func (s *gcsSeedStore) Upload(ctx context.Context, snapshot []byte, completion string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	obj := s.client.Bucket(s.opts.Bucket).Object(s.opts.objectKey()).If(storage.Conditions{DoesNotExist: true})
	w := obj.NewWriter(ctx)
	w.Metadata = map[string]string{seedCompletionMetadata: completion}
	if _, err := io.Copy(w, bytes.NewReader(snapshot)); err != nil {
		cancel()
		_ = w.Close()
		return errors.New("GCS create-only seed upload failed")
	}
	if err := w.Close(); err != nil {
		return errors.New("GCS create-only seed upload failed")
	}
	return nil
}

// newGCSClient returns a GCS client using Application Default Credentials.
func newGCSClient(ctx context.Context, opts SeedOptions) (*storage.Client, error) {
	clientOpts := []option.ClientOption{}
	if opts.Project != "" {
		clientOpts = append(clientOpts, option.WithQuotaProject(opts.Project))
	}
	client, err := storage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return client, nil
}

// requireEmptyGCSPrefix fails when any object already exists under prefix.
func requireEmptyGCSPrefix(ctx context.Context, client *storage.Client, bucket, prefix string) error {
	query := &storage.Query{}
	if prefix != "" {
		query.Prefix = prefix + "/"
	}
	it := client.Bucket(bucket).Objects(ctx, query)
	_, err := it.Next()
	if err == iterator.Done {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check GCS prefix gs://%s/%s is empty: %w", bucket, prefix, err)
	}
	return fmt.Errorf("GCS prefix gs://%s/%s already contains Netsy state or other objects", bucket, prefix)
}
