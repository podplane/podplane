// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfstate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/cloudresourcemanager/v1"
)

// googleAccessError explains ADC failures without mistaking permission or
// connectivity errors for expired credentials. Keep the original error wrapped.
func googleAccessError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, "invalid_grant") || strings.Contains(message, "invalid_rapt") || strings.Contains(message, "could not find default credentials") {
		return fmt.Errorf("application credentials for Google Cloud need attention (missing or expired, or reauthentication required).\nRun `gcloud auth application-default login`, then retry.\nIf GOOGLE_APPLICATION_CREDENTIALS is set, refresh its credentials instead.\nDetails: %w", err)
	}
	return err
}

// checkGCS verifies an existing GCS state bucket belonging to the backend
// project, or reports errBucketAbsent when it does not exist.
func (b Backend) checkGCS(ctx context.Context, warn io.Writer) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create Google Cloud Storage client for remote state: %w", err)
	}
	defer func() { _ = client.Close() }()
	bucket := client.Bucket(b.Bucket)
	attrs, err := bucket.Attrs(ctx)
	if err == nil {
		if err := b.ownedGCS(ctx, attrs); err != nil {
			return err
		}
		if err := b.verifyIdentity(attrs.Labels); err != nil {
			return err
		}
		if !attrs.VersioningEnabled {
			_, _ = fmt.Fprintf(warn, "Warning: remote-state bucket gs://%s does not have versioning enabled; versioning is recommended for state recovery\n", b.Bucket)
		}
		if !attrs.UniformBucketLevelAccess.Enabled || attrs.PublicAccessPrevention != storage.PublicAccessPreventionEnforced {
			return fmt.Errorf("existing remote-state bucket gs://%s must enable uniform access and public-access prevention", b.Bucket)
		}
		return nil
	}
	if !errors.Is(err, storage.ErrBucketNotExist) {
		return fmt.Errorf("check remote-state bucket gs://%s: %w", b.Bucket, err)
	}
	return fmt.Errorf("remote-state bucket gs://%s %w", b.Bucket, errBucketAbsent)
}

// ownedGCS requires the bucket described by attrs to belong to the backend
// project, so a same-named bucket in another project is never used for state.
func (b Backend) ownedGCS(ctx context.Context, attrs *storage.BucketAttrs) error {
	manager, err := cloudresourcemanager.NewService(ctx)
	if err != nil {
		return fmt.Errorf("create Resource Manager client for remote state: %w", err)
	}
	project, err := manager.Projects.Get(b.Project).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("read Google Cloud project %s for remote state: %w", b.Project, err)
	}
	if attrs.ProjectNumber == 0 || attrs.ProjectNumber != uint64(project.ProjectNumber) {
		return fmt.Errorf("existing remote-state bucket gs://%s does not belong to project %s", b.Bucket, b.Project)
	}
	return nil
}

// createGCS creates a private, versioned GCS state bucket with the recovery
// lifecycle, accepting a bucket that another process created concurrently.
func (b Backend) createGCS(ctx context.Context) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create Google Cloud Storage client for remote state: %w", err)
	}
	defer func() { _ = client.Close() }()
	attrs := &storage.BucketAttrs{
		Lifecycle: storage.Lifecycle{Rules: []storage.LifecycleRule{{
			Action:    storage.LifecycleAction{Type: storage.DeleteAction},
			Condition: storage.LifecycleCondition{DaysSinceNoncurrentTime: int64(remoteStateRecoveryDays), Liveness: storage.Archived, MatchesPrefix: []string{"podplane/"}},
		}}},
		Location:                 b.Region,
		Labels:                   b.identityMarkers(),
		PublicAccessPrevention:   storage.PublicAccessPreventionEnforced,
		UniformBucketLevelAccess: storage.UniformBucketLevelAccess{Enabled: true},
		VersioningEnabled:        true,
	}
	if err := client.Bucket(b.Bucket).Create(ctx, b.Project, attrs); err != nil {
		if checkErr := b.checkGCS(ctx, io.Discard); checkErr == nil {
			return nil
		}
		return fmt.Errorf("create remote-state bucket gs://%s: %w", b.Bucket, err)
	}
	return nil
}
