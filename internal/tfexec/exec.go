// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	binary        string
	cliConfigFile string
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

// InitBackendDisabled initializes dependencies without configuring a backend.
func (c *CLI) InitBackendDisabled(ctx context.Context, dir string) error {
	return c.run(ctx, dir, "init", "-backend=false")
}

// ProvidersMirror downloads provider packages for platform into mirrorDir.
func (c *CLI) ProvidersMirror(ctx context.Context, dir, mirrorDir, platform string) error {
	return c.run(ctx, dir, "providers", "mirror", "-platform="+platform, mirrorDir)
}

// ProvidersLock records provider checksums for platforms from mirrorDir.
func (c *CLI) ProvidersLock(ctx context.Context, dir, mirrorDir string, platforms []string) error {
	args := []string{"providers", "lock", "-fs-mirror=" + mirrorDir}
	for _, platform := range platforms {
		args = append(args, "-platform="+platform)
	}
	return c.run(ctx, dir, args...)
}

// Platform returns the platform reported by the selected engine.
func (c *CLI) Platform(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, c.binary, "version", "-json")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read OpenTofu/Terraform platform: %w", err)
	}
	var version struct {
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal(out, &version); err != nil {
		return "", fmt.Errorf("decode OpenTofu/Terraform version: %w", err)
	}
	if version.Platform == "" {
		return "", fmt.Errorf("OpenTofu/Terraform version did not report a platform")
	}
	return version.Platform, nil
}

// WithCLIConfig returns a copy that uses path as TF_CLI_CONFIG_FILE.
func (c *CLI) WithCLIConfig(path string) *CLI {
	configured := *c
	configured.cliConfigFile = path
	return &configured
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
	if c.cliConfigFile != "" {
		cmd.Env = withoutEnv(os.Environ(), "TF_CLI_CONFIG_FILE")
		cmd.Env = append(cmd.Env, "TF_CLI_CONFIG_FILE="+c.cliConfigFile)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run OpenTofu/Terraform %v in %s: %w", args, dir, err)
	}
	return nil
}

// withoutEnv returns env without entries for name.
func withoutEnv(env []string, name string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env))
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}
