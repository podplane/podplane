// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"

	"github.com/podplane/podplane/internal/clusterdelete"
)

// stateLock identifies the GCS object generation owned by this teardown.
type stateLock struct {
	provider    *Provider
	bucket, key string
	generation  int64
	emptied     bool
}

// LockState acquires the same generation-conditional lock as the GCS engine backend.
func (p *Provider) LockState(ctx context.Context, bucket, key string) (clusterdelete.StateLock, error) {
	body, err := clusterdelete.StateLockInfo("gs://" + bucket + "/" + key)
	if err != nil {
		return nil, err
	}
	w := p.storage.Bucket(bucket).Object(key).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ContentType = "application/json"
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("write state lock gs://%s/%s: %w", bucket, key, err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("acquire state lock gs://%s/%s (inspect the lock before retrying): %w", bucket, key, err)
	}
	return &stateLock{provider: p, bucket: bucket, key: key, generation: w.Attrs().Generation}, nil
}

// EmptyBucket removes archived and live generations, preserving our lock until release.
func (l *stateLock) EmptyBucket(ctx context.Context) error {
	if err := l.provider.emptyBucket(ctx, l.bucket, l.key, l.generation); err != nil {
		return err
	}
	l.emptied = true
	return nil
}

// Unlock removes our lock with a generation precondition, leaving replacement locks alone.
func (l *stateLock) Unlock(ctx context.Context) error {
	object := l.provider.storage.Bucket(l.bucket).Object(l.key)
	if l.emptied {
		// Explicit generation deletion does not archive the last live lock.
		object = object.Generation(l.generation)
	}
	if err := object.If(storage.Conditions{GenerationMatch: l.generation}).Delete(ctx); err != nil {
		return fmt.Errorf("release state lock gs://%s/%s (generation %d): %w", l.bucket, l.key, l.generation, err)
	}
	return nil
}
