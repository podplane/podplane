// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"

	"cloud.google.com/go/storage"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterdelete"
)

// Provider implements clusterdelete.Provider with Cloud Storage and Compute
// Engine using Application Default Credentials.
type Provider struct {
	storage *storage.Client
	compute *compute.Service
	project string
}

// New creates a Google Cloud delete provider for the cluster's project.
func New(ctx context.Context, provider clusterconfig.Provider) (*Provider, error) {
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create Google Cloud Storage client: %w", err)
	}
	computeService, err := compute.NewService(ctx)
	if err != nil {
		_ = storageClient.Close()
		return nil, fmt.Errorf("create Compute Engine client: %w", err)
	}
	return &Provider{storage: storageClient, compute: computeService, project: provider.Project}, nil
}

// Close releases the Cloud Storage client.
func (p *Provider) Close() error {
	return p.storage.Close()
}

// UpdateObject rewrites a Cloud Storage object, conditional on its generation.
func (p *Provider) UpdateObject(ctx context.Context, bucket, key string, update func([]byte) ([]byte, bool, error)) error {
	obj := p.storage.Bucket(bucket).Object(key)
	r, err := obj.NewReader(ctx)
	if err != nil {
		return fmt.Errorf("get gs://%s/%s: %w", bucket, key, err)
	}
	body, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		return fmt.Errorf("read gs://%s/%s: %w", bucket, key, err)
	}
	next, changed, err := update(body)
	if err != nil || !changed {
		return err
	}
	w := obj.If(storage.Conditions{GenerationMatch: r.Attrs.Generation}).NewWriter(ctx)
	w.ContentType = "application/json"
	if _, err := w.Write(next); err != nil {
		_ = w.Close()
		return fmt.Errorf("put gs://%s/%s: %w", bucket, key, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("put gs://%s/%s: %w", bucket, key, err)
	}
	return nil
}

// ManagedInstances lists Compute Engine instances in the project labelled
// nstance-cluster-id=clusterID and nstance-managed=true.
func (p *Provider) ManagedInstances(ctx context.Context, clusterID string) ([]clusterdelete.Instance, error) {
	filter := fmt.Sprintf(`labels.nstance-cluster-id = "%s" AND labels.nstance-managed = "true"`, clusterID)
	var out []clusterdelete.Instance
	err := p.compute.Instances.AggregatedList(p.project).Filter(filter).Pages(ctx, func(page *compute.InstanceAggregatedList) error {
		for _, scoped := range page.Items {
			for _, inst := range scoped.Instances {
				out = append(out, clusterdelete.Instance{
					ID:    inst.Name,
					Zone:  path.Base(inst.Zone),
					Shard: inst.Labels["nstance-shard"],
					Group: inst.Labels["nstance-group"],
					State: inst.Status,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list Nstance-managed Compute Engine instances labelled nstance-cluster-id=%s: %w", clusterID, err)
	}
	return out, nil
}

// Terminate deletes Compute Engine instances, ignoring ones already gone.
func (p *Provider) Terminate(ctx context.Context, instances []clusterdelete.Instance) error {
	for _, inst := range instances {
		_, err := p.compute.Instances.Delete(p.project, inst.Zone, inst.ID).Context(ctx).Do()
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			continue
		}
		if err != nil {
			return fmt.Errorf("delete Compute Engine instance %s in %s: %w", inst.ID, inst.Zone, err)
		}
	}
	return nil
}
