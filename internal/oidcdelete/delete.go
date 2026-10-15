// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcdelete

import (
	"context"
	"fmt"
	"time"
)

// Executor destroys OpenTofu/Terraform-managed OIDC resources.
type Executor interface {
	Destroy(ctx context.Context, dir string, autoApprove bool) error
}

// ConfirmFunc asks the user to confirm a destructive action.
type ConfirmFunc func(message string) (bool, error)

// DeleteOptions supplies the stack, executor, confirmation policy, and destroy deadline.
type DeleteOptions struct {
	StackDir       string
	Executor       Executor
	AutoApprove    bool
	Confirm        ConfirmFunc
	DestroyTimeout time.Duration
}

// Delete confirms and destroys OpenTofu/Terraform-managed OIDC resources.
func Delete(ctx context.Context, opts DeleteOptions) error {
	if opts.Executor == nil {
		return fmt.Errorf("OpenTofu/Terraform executor is required")
	}
	confirm := opts.Confirm
	if confirm == nil {
		confirm = func(string) (bool, error) {
			return false, fmt.Errorf("confirmation callback is required")
		}
	}
	ok, err := confirm("Destroy OpenTofu/Terraform-managed resources?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("destroy cancelled")
	}
	destroyTimeout := opts.DestroyTimeout
	if destroyTimeout == 0 {
		destroyTimeout = 2 * time.Hour
	}
	destroyCtx, cancel := context.WithTimeout(ctx, destroyTimeout)
	defer cancel()
	return opts.Executor.Destroy(destroyCtx, opts.StackDir, opts.AutoApprove)
}
