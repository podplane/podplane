// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// Executor provides Terraform outputs and destroy behavior for cluster delete.
type Executor interface {
	OutputJSON(ctx context.Context, dir string) ([]byte, error)
	Destroy(ctx context.Context, dir string, autoApprove bool) error
}

// Provider performs provider-specific cleanup before and after destroy. Delete
// runs the cleanup workflow, which is the same on every cloud, and calls
// Provider for the cloud operations it needs.
type Provider interface {
	// UpdateObject replaces the object at key in bucket with the result of
	// update unless update reports no change. It must not overwrite a
	// concurrent change to the object.
	UpdateObject(ctx context.Context, bucket, key string, update func([]byte) ([]byte, bool, error)) error

	// ManagedInstances lists the Nstance-managed VM instances labelled for
	// clusterID that have not terminated.
	ManagedInstances(ctx context.Context, clusterID string) ([]Instance, error)

	// Terminate deletes instances.
	Terminate(ctx context.Context, instances []Instance) error
}

// Instance describes a provider VM instance that may need cleanup.
type Instance struct {
	ID    string
	Name  string
	Zone  string
	Shard string
	Group string
	State string
}

// ConfirmFunc asks the user to confirm a destructive action.
type ConfirmFunc func(message string) (bool, error)

// StatusFunc reports delete workflow status to the caller.
type StatusFunc func(message string)

const (
	// outputTimeout bounds reading OpenTofu/Terraform outputs.
	outputTimeout = 30 * time.Minute
	// destroyTimeout bounds OpenTofu/Terraform destroy.
	destroyTimeout = 2 * time.Hour
)

// DeleteOptions controls the cluster delete workflow. Status may be nil; every
// other field is required.
type DeleteOptions struct {
	ClusterConfig *clusterconfig.ClusterConfig
	StackDir      string
	Executor      Executor
	Provider      Provider
	AutoApprove   bool
	Confirm       ConfirmFunc
	Status        StatusFunc
	// AfterDestroy runs only after all remaining managed instances have terminated.
	AfterDestroy func(context.Context) error
	// WaitTimeout is how long to wait for Nstance-managed instances to
	// terminate after scale-down, and again after direct termination.
	WaitTimeout time.Duration
	// PollInterval is how often to list instances while waiting.
	PollInterval time.Duration
}

// Delete scales Nstance-managed groups to zero, waits for their instances to
// terminate, destroys OpenTofu/Terraform-managed resources, and offers to
// terminate any Nstance-managed instances that appeared meanwhile.
func Delete(ctx context.Context, opts DeleteOptions) error {
	if opts.ClusterConfig == nil {
		return fmt.Errorf("cluster config is required")
	}
	if opts.Executor == nil {
		return fmt.Errorf("OpenTofu/Terraform executor is required")
	}
	if opts.Provider == nil {
		return fmt.Errorf("cluster delete provider is required")
	}
	if opts.Confirm == nil {
		return fmt.Errorf("confirmation callback is required")
	}
	confirm := opts.Confirm
	status := opts.Status
	if status == nil {
		status = func(string) {}
	}
	clusterID := opts.ClusterConfig.Cluster.ID

	outputCtx, outputCancel := context.WithTimeout(ctx, outputTimeout)
	defer outputCancel()
	raw, err := opts.Executor.OutputJSON(outputCtx, opts.StackDir)
	if err != nil {
		return err
	}
	bucket, shards, err := parseOutputs(raw)
	if err != nil {
		return err
	}

	status("Scaling Nstance-managed groups to zero in shard configs.")
	for _, shard := range slices.Sorted(maps.Keys(shards)) {
		if err := opts.Provider.UpdateObject(ctx, bucket, shards[shard], zeroShardConfig); err != nil {
			return fmt.Errorf("scale shard %s groups to zero: %w", shard, err)
		}
	}

	wait := func() ([]Instance, error) {
		return waitForManagedInstances(ctx, opts.Provider, clusterID, opts.WaitTimeout, opts.PollInterval)
	}
	status("Waiting for Nstance-managed instances to terminate.")
	remaining, err := wait()
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		status(fmt.Sprintf("Nstance-managed instances are still present after scale-down: %s", formatInstances(remaining)))
		ok, err := confirm("Terminate remaining Nstance-managed instances directly before destroy?")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%d Nstance-managed instance(s) still present", len(remaining))
		}
		if err := opts.Provider.Terminate(ctx, remaining); err != nil {
			return err
		}
		remaining, err = wait()
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			return fmt.Errorf("timed out waiting for %d Nstance-managed instance(s) to terminate: %s", len(remaining), formatInstances(remaining))
		}
	}

	ok, err := confirm("Destroy OpenTofu/Terraform-managed resources?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("destroy cancelled")
	}
	destroyCtx, destroyCancel := context.WithTimeout(ctx, destroyTimeout)
	defer destroyCancel()
	if err := opts.Executor.Destroy(destroyCtx, opts.StackDir, opts.AutoApprove); err != nil {
		return err
	}

	dangling, err := opts.Provider.ManagedInstances(ctx, clusterID)
	if err != nil {
		return err
	}
	if len(dangling) == 0 {
		if opts.AfterDestroy != nil {
			return opts.AfterDestroy(ctx)
		}
		return nil
	}
	status(fmt.Sprintf("Found dangling Nstance-managed instances for cluster %q: %s", clusterID, formatInstances(dangling)))
	ok, err = confirm("Terminate dangling Nstance-managed instances?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("dangling instance cleanup cancelled")
	}
	if err := opts.Provider.Terminate(ctx, dangling); err != nil {
		return err
	}
	remaining, err = wait()
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf("instances remain after destroy; storage was not cleaned up")
	}
	if opts.AfterDestroy != nil {
		return opts.AfterDestroy(ctx)
	}
	return nil
}

// parseOutputs extracts the Nstance bucket and each shard's config key from
// OpenTofu/Terraform output JSON.
func parseOutputs(raw []byte) (string, map[string]string, error) {
	var outputs struct {
		Bucket struct {
			Value string `json:"value"`
		} `json:"nstance_bucket"`
		Shards struct {
			Value map[string]struct {
				ConfigKey string `json:"config_key"`
			} `json:"value"`
		} `json:"nstance_shards"`
	}
	if err := json.Unmarshal(raw, &outputs); err != nil {
		return "", nil, fmt.Errorf("parse OpenTofu/Terraform outputs: %w", err)
	}
	const regenerate = "rerun podplane cluster create --no-apply to regenerate managed .tf files and apply them"
	// A previous destroy may already have removed the shard outputs.
	if len(outputs.Shards.Value) == 0 {
		return outputs.Bucket.Value, map[string]string{}, nil
	}
	if outputs.Bucket.Value == "" {
		return "", nil, fmt.Errorf("missing nstance_bucket output; %s", regenerate)
	}
	shards := make(map[string]string, len(outputs.Shards.Value))
	for shard, out := range outputs.Shards.Value {
		if out.ConfigKey == "" {
			return "", nil, fmt.Errorf("shard %s has empty config_key output", shard)
		}
		shards[shard] = out.ConfigKey
	}
	return outputs.Bucket.Value, shards, nil
}

// zeroShardConfig sets every group size in a shard config document to zero
// and reports whether anything changed.
func zeroShardConfig(raw []byte) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false, fmt.Errorf("parse shard config: %w", err)
	}
	groups, _ := doc["groups"].(map[string]any)
	changed := false
	for _, tenant := range groups {
		tenant, _ := tenant.(map[string]any)
		for _, group := range tenant {
			group, ok := group.(map[string]any)
			if ok && group["size"] != float64(0) {
				group["size"] = float64(0)
				changed = true
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	next, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("marshal shard config: %w", err)
	}
	return next, true, nil
}

// waitForManagedInstances polls until no Nstance-managed instances remain or
// timeout passes, bounding both requests and sleeps. On timeout it returns
// the last successful instance list for direct termination; without a known
// list it returns the request error instead of proceeding to destroy.
func waitForManagedInstances(ctx context.Context, provider Provider, clusterID string, timeout, interval time.Duration) ([]Instance, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var remaining []Instance
	for {
		instances, err := provider.ManagedInstances(waitCtx, clusterID)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			if waitCtx.Err() == context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded) && len(remaining) > 0 {
				return remaining, nil
			}
			return nil, err
		}
		remaining = instances
		if len(remaining) == 0 {
			return nil, nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return remaining, nil
		case <-time.After(interval):
		}
	}
}

// formatInstances formats provider instances for human-readable status and
// confirmation messages.
func formatInstances(instances []Instance) string {
	parts := make([]string, 0, len(instances))
	for _, inst := range instances {
		label := inst.ID
		if inst.Name != "" {
			label += " (" + inst.Name + ")"
		}
		for _, attr := range []struct{ key, value string }{{"zone", inst.Zone}, {"shard", inst.Shard}, {"group", inst.Group}, {"state", inst.State}} {
			if attr.value != "" {
				label += " " + attr.key + "=" + attr.value
			}
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
