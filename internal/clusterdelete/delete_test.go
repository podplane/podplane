// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"context"
	"errors"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// TestDeleteTerminatesRemainingManagedInstances verifies that delete offers a
// direct termination fallback when managed instances remain after scale-down.
func TestDeleteTerminatesRemainingManagedInstances(t *testing.T) {
	executor := &fakeExecutor{outputs: []byte(`{}`)}
	provider := &fakeProvider{
		waitErrs: []error{errors.New("instances still running"), nil},
		remaining: []Instance{
			{ID: "i-123", Name: "control-plane"},
		},
	}
	var confirmations []string
	err := Delete(context.Background(), DeleteOptions{
		ClusterConfig: &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "test-cluster"}},
		StackDir:      "/tmp/stack",
		Executor:      executor,
		Provider:      provider,
		Confirm: func(message string) (bool, error) {
			confirmations = append(confirmations, message)
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if !executor.destroyed {
		t.Fatal("Destroy was not called")
	}
	if provider.terminated != 1 {
		t.Fatalf("terminated = %d, want 1", provider.terminated)
	}
	if len(confirmations) != 2 {
		t.Fatalf("confirmations = %v, want remaining and destroy confirmations", confirmations)
	}
}

// TestDeleteTerminatesDanglingInstancesAfterDestroy verifies that delete can
// clean up tagged instances left after Terraform destroy.
func TestDeleteTerminatesDanglingInstancesAfterDestroy(t *testing.T) {
	executor := &fakeExecutor{outputs: []byte(`{}`)}
	provider := &fakeProvider{
		dangling: []Instance{{ID: "i-dangling"}},
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
	if provider.terminated != 1 {
		t.Fatalf("terminated = %d, want dangling instance terminated", provider.terminated)
	}
}

// fakeExecutor supplies Terraform outputs and records resource destruction.
type fakeExecutor struct {
	outputs   []byte
	destroyed bool
}

// OutputJSON returns canned Terraform outputs for delete workflow tests.
func (f *fakeExecutor) OutputJSON(context.Context, string) ([]byte, error) {
	return f.outputs, nil
}

// Destroy records that the fake executor was asked to destroy resources.
func (f *fakeExecutor) Destroy(context.Context, string, bool) error {
	f.destroyed = true
	return nil
}

// fakeProvider supplies instance lists and tracks termination and queued wait failures.
type fakeProvider struct {
	waitErrs   []error
	remaining  []Instance
	dangling   []Instance
	terminated int
}

// Prepare satisfies the provider interface without extra setup.
func (f *fakeProvider) Prepare(context.Context, *clusterconfig.ClusterConfig, []byte) error {
	return nil
}

// ScaleManagedGroupsToZero satisfies the provider interface without changing
// fake state.
func (f *fakeProvider) ScaleManagedGroupsToZero(context.Context) error {
	return nil
}

// WaitForManagedInstancesGone returns queued wait errors for fallback tests.
func (f *fakeProvider) WaitForManagedInstancesGone(context.Context) error {
	if len(f.waitErrs) == 0 {
		return nil
	}
	err := f.waitErrs[0]
	f.waitErrs = f.waitErrs[1:]
	return err
}

// RemainingManagedInstances returns canned managed instances for fallback
// tests.
func (f *fakeProvider) RemainingManagedInstances(context.Context) ([]Instance, error) {
	return f.remaining, nil
}

// TerminateInstances records how many instances the workflow asked to
// terminate.
func (f *fakeProvider) TerminateInstances(_ context.Context, instances []Instance) error {
	f.terminated += len(instances)
	return nil
}

// ListTaggedInstances returns canned dangling instances unless the caller asks
// only for managed instances.
func (f *fakeProvider) ListTaggedInstances(_ context.Context, managedOnly bool) ([]Instance, error) {
	if managedOnly {
		return nil, nil
	}
	return f.dangling, nil
}

// ClusterID returns a stable fake cluster ID for status messages.
func (f *fakeProvider) ClusterID() string {
	return "test-cluster"
}
