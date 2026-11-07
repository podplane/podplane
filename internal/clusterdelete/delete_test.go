// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// testOutputs is OpenTofu/Terraform output JSON for a cluster with two shards.
const testOutputs = `{
  "nstance_bucket": {"sensitive": false, "type": "string", "value": "cluster-bucket"},
  "nstance_shards": {"sensitive": false, "type": ["object", {}], "value": {
    "zone-a": {"config_key": "shard/zone-a/config.jsonc", "server_ips": ["10.0.0.10"]},
    "zone-b": {"config_key": "shard/zone-b/config.jsonc", "server_ips": ["10.0.1.10"]}
  }}
}`

// TestDeleteScalesDownAndTerminatesRemainingManagedInstances verifies that
// delete zeroes every shard's groups and offers direct termination when
// managed instances remain after scale-down.
func TestDeleteScalesDownAndTerminatesRemainingManagedInstances(t *testing.T) {
	executor := &fakeExecutor{outputs: []byte(testOutputs)}
	provider := &fakeProvider{
		objects: map[string]string{
			"cluster-bucket/shard/zone-a/config.jsonc": `{"groups": {"default": {"control-plane": {"size": 3}, "workers": {"size": 0}}}}`,
			"cluster-bucket/shard/zone-b/config.jsonc": `{"groups": {"default": {"workers": {"size": 0}}}}`,
		},
		managed: []Instance{{ID: "i-123", Name: "control-plane"}},
	}
	var confirmations []string
	err := Delete(context.Background(), DeleteOptions{
		ClusterConfig: &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "test-cluster"}},
		StackDir:      "/tmp/stack",
		Executor:      executor,
		Provider:      provider,
		WaitTimeout:   time.Millisecond,
		PollInterval:  time.Millisecond,
		Confirm: func(message string) (bool, error) {
			confirmations = append(confirmations, message)
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	var doc struct {
		Groups map[string]map[string]struct{ Size int } `json:"groups"`
	}
	if err := json.Unmarshal([]byte(provider.objects["cluster-bucket/shard/zone-a/config.jsonc"]), &doc); err != nil {
		t.Fatal(err)
	}
	for name, group := range doc.Groups["default"] {
		if group.Size != 0 {
			t.Fatalf("zone-a group %s size = %d, want 0", name, group.Size)
		}
	}
	if len(provider.writes) != 1 || provider.writes[0] != "cluster-bucket/shard/zone-a/config.jsonc" {
		t.Fatalf("writes = %v, want only the zone-a config rewritten", provider.writes)
	}
	if len(provider.terminated) != 1 || provider.terminated[0] != "i-123" {
		t.Fatalf("terminated = %v, want i-123", provider.terminated)
	}
	if !executor.destroyed {
		t.Fatal("Destroy was not called")
	}
	if len(confirmations) != 2 {
		t.Fatalf("confirmations = %v, want remaining and destroy confirmations", confirmations)
	}
}

// TestDeleteTerminatesDanglingInstancesAfterDestroy verifies that delete can
// clean up Nstance-managed instances that appear during Terraform destroy.
func TestDeleteTerminatesDanglingInstancesAfterDestroy(t *testing.T) {
	provider := &fakeProvider{
		objects: map[string]string{
			"cluster-bucket/shard/zone-a/config.jsonc": `{}`,
			"cluster-bucket/shard/zone-b/config.jsonc": `{}`,
		},
	}
	executor := &fakeExecutor{
		outputs:   []byte(testOutputs),
		onDestroy: func() { provider.managed = []Instance{{ID: "i-dangling"}} },
	}
	err := Delete(context.Background(), DeleteOptions{
		ClusterConfig: &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "test-cluster"}},
		StackDir:      "/tmp/stack",
		Executor:      executor,
		Provider:      provider,
		Confirm: func(string) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(provider.terminated) != 1 || provider.terminated[0] != "i-dangling" {
		t.Fatalf("terminated = %v, want dangling instance terminated", provider.terminated)
	}
}

// TestDeleteBoundsInstancePolling checks request and sleep deadlines, safe
// fallback with a known instance list, and cancellation/error propagation.
func TestDeleteBoundsInstancePolling(t *testing.T) {
	apiError := errors.New("instance listing denied")
	for _, tt := range []struct {
		name     string
		wantErr  error
		wantTime time.Duration
	}{
		{"first request stalls", context.DeadlineExceeded, 20 * time.Minute},
		{"later request stalls", nil, 20 * time.Minute},
		{"poll interval exceeds timeout", nil, 20 * time.Minute},
		{"parent cancelled", context.Canceled, time.Minute},
		{"parent deadline", context.DeadlineExceeded, time.Minute},
		{"API failure", apiError, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
				defer cancel()
				if tt.name == "parent cancelled" {
					time.AfterFunc(time.Minute, cancel)
				}
				if tt.name == "parent deadline" {
					var cancelDeadline context.CancelFunc
					ctx, cancelDeadline = context.WithTimeout(ctx, time.Minute)
					defer cancelDeadline()
				}
				provider := &fakeProvider{
					objects: map[string]string{
						"cluster-bucket/shard/zone-a/config.jsonc": `{}`,
						"cluster-bucket/shard/zone-b/config.jsonc": `{}`,
					},
					managed: []Instance{{ID: "i-remaining"}},
				}
				listed := false
				provider.list = func(ctx context.Context) ([]Instance, error) {
					if tt.name == "API failure" {
						return nil, apiError
					}
					if len(provider.managed) == 0 || tt.name == "poll interval exceeds timeout" {
						return provider.managed, nil
					}
					if tt.name == "later request stalls" && !listed {
						listed = true
						return provider.managed, nil
					}
					<-ctx.Done()
					return nil, fmt.Errorf("list instances: %w", ctx.Err())
				}
				interval := time.Second
				if tt.name == "poll interval exceeds timeout" {
					interval = time.Hour
				}
				executor := &fakeExecutor{outputs: []byte(testOutputs)}
				started := time.Now()
				err := Delete(ctx, DeleteOptions{
					ClusterConfig: &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "test-cluster"}},
					Executor:      executor, Provider: provider,
					WaitTimeout: 20 * time.Minute, PollInterval: interval,
					Confirm: func(string) (bool, error) { return true, nil },
				})
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Delete error = %v, want %v", err, tt.wantErr)
				}
				if elapsed := time.Since(started); elapsed != tt.wantTime {
					t.Fatalf("Delete took %v, want %v", elapsed, tt.wantTime)
				}
				if tt.wantErr != nil {
					if executor.destroyed || len(provider.terminated) != 0 {
						t.Fatal("delete continued destructive operations after an error")
					}
				} else if !executor.destroyed || len(provider.terminated) != 1 || provider.terminated[0] != "i-remaining" {
					t.Fatal("wait timeout did not allow direct termination and destroy")
				}
			})
		})
	}
}

// fakeExecutor supplies Terraform outputs and records resource destruction.
type fakeExecutor struct {
	outputs   []byte
	destroyed bool
	// onDestroy, when set, runs during Destroy.
	onDestroy func()
}

// OutputJSON returns canned Terraform outputs for delete workflow tests.
func (f *fakeExecutor) OutputJSON(context.Context, string) ([]byte, error) {
	return f.outputs, nil
}

// Destroy records that the fake executor was asked to destroy resources.
func (f *fakeExecutor) Destroy(context.Context, string, bool) error {
	f.destroyed = true
	if f.onDestroy != nil {
		f.onDestroy()
	}
	return nil
}

// fakeProvider stores objects in memory and lists managed instances until
// they are terminated.
type fakeProvider struct {
	objects    map[string]string
	writes     []string
	managed    []Instance
	terminated []string
	list       func(context.Context) ([]Instance, error)
}

// UpdateObject applies update to an in-memory object and records writes.
func (f *fakeProvider) UpdateObject(_ context.Context, bucket, key string, update func([]byte) ([]byte, bool, error)) error {
	name := bucket + "/" + key
	next, changed, err := update([]byte(f.objects[name]))
	if err != nil || !changed {
		return err
	}
	f.objects[name] = string(next)
	f.writes = append(f.writes, name)
	return nil
}

// ManagedInstances returns the managed instances not yet terminated.
func (f *fakeProvider) ManagedInstances(ctx context.Context, _ string) ([]Instance, error) {
	if f.list != nil {
		return f.list(ctx)
	}
	return f.managed, nil
}

// Terminate records terminated instance IDs and removes them from listings.
func (f *fakeProvider) Terminate(_ context.Context, instances []Instance) error {
	for _, inst := range instances {
		f.terminated = append(f.terminated, inst.ID)
	}
	f.managed = nil
	return nil
}
