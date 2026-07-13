// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// CommandEnvVar selects the OpenTofu or Terraform command used by Podplane.
const CommandEnvVar = "PODPLANE_TF_CMD"

// Executor runs OpenTofu/Terraform operations for a generated stack.
type Executor interface {
	Init(ctx context.Context, dir string) error
	Apply(ctx context.Context, dir string, autoApprove bool) error
	Destroy(ctx context.Context, dir string, autoApprove bool) error
	OutputJSON(ctx context.Context, dir string) ([]byte, error)
}

// CLI runs OpenTofu/Terraform by invoking a local executable.
type CLI struct {
	binary string
}

// NewCLI selects the configured executable or finds tofu or terraform on PATH.
func NewCLI() (*CLI, error) {
	if executable := os.Getenv(CommandEnvVar); executable != "" {
		path, err := exec.LookPath(executable)
		if err != nil {
			return nil, fmt.Errorf("%s command %q not found: %w", CommandEnvVar, executable, err)
		}
		return &CLI{binary: path}, nil
	}
	if path, err := exec.LookPath("tofu"); err == nil {
		return &CLI{binary: path}, nil
	}
	if path, err := exec.LookPath("terraform"); err == nil {
		return &CLI{binary: path}, nil
	}
	return nil, fmt.Errorf("OpenTofu/Terraform executable not found on PATH; install tofu or terraform")
}

// Init runs OpenTofu/Terraform init in dir.
func (c *CLI) Init(ctx context.Context, dir string) error {
	return c.run(ctx, dir, "init")
}

// Apply runs OpenTofu/Terraform apply in dir.
func (c *CLI) Apply(ctx context.Context, dir string, autoApprove bool) error {
	args := []string{"apply"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	return c.run(ctx, dir, args...)
}

// Destroy runs OpenTofu/Terraform destroy in dir.
func (c *CLI) Destroy(ctx context.Context, dir string, autoApprove bool) error {
	args := []string{"destroy"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	return c.run(ctx, dir, args...)
}

// OutputJSON runs OpenTofu/Terraform output -json in dir.
func (c *CLI) OutputJSON(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binary, "output", "-json")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run OpenTofu/Terraform output -json in %s: %w", dir, err)
	}
	return out, nil
}

// run invokes the configured executable with args in dir.
func (c *CLI) run(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, c.binary, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run OpenTofu/Terraform %v in %s: %w", args, dir, err)
	}
	return nil
}
