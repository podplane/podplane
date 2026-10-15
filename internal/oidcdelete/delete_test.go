// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcdelete

import (
	"context"
	"testing"
)

// TestDeleteDestroysAfterConfirmation verifies that destroy runs after
// confirmation.
func TestDeleteDestroysAfterConfirmation(t *testing.T) {
	executor := &fakeExecutor{}
	err := Delete(context.Background(), DeleteOptions{
		StackDir: "/tmp/stack",
		Executor: executor,
		Confirm:  func(string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if !executor.destroyed {
		t.Fatal("Destroy was not called")
	}
}

// TestDeleteCancelled verifies that destroy is skipped when confirmation is
// denied.
func TestDeleteCancelled(t *testing.T) {
	executor := &fakeExecutor{}
	err := Delete(context.Background(), DeleteOptions{
		StackDir: "/tmp/stack",
		Executor: executor,
		Confirm:  func(string) (bool, error) { return false, nil },
	})
	if err == nil {
		t.Fatal("Delete returned nil, want cancellation error")
	}
	if executor.destroyed {
		t.Fatal("Destroy was called after cancellation")
	}
}

// fakeExecutor records whether the OIDC delete workflow requested resource destruction.
type fakeExecutor struct {
	destroyed bool
}

// Destroy records that the fake executor was asked to destroy resources.
func (f *fakeExecutor) Destroy(context.Context, string, bool) error {
	f.destroyed = true
	return nil
}
