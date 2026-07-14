// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIInvokesTofuWithExpectedArguments verifies the CLI executor uses a tofu
// executable found on PATH and passes expected apply/destroy/output arguments.
func TestCLIInvokesTofuWithExpectedArguments(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tofu")
	logPath := filepath.Join(dir, "args.log")
	quotedLogPath := "'" + strings.ReplaceAll(logPath, "'", "'\\''") + "'"
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + quotedLogPath + "\nif [ \"$1\" = output ]; then printf '{}'; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(CommandEnvVar, "")
	cli, err := NewCLI()
	if err != nil {
		t.Fatalf("NewCLI returned error: %v", err)
	}
	ctx := context.Background()
	if err := cli.Init(ctx, dir); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if err := cli.Apply(ctx, dir, true); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if err := cli.Destroy(ctx, dir, true); err != nil {
		t.Fatalf("Destroy returned error: %v", err)
	}
	if _, err := cli.OutputJSON(ctx, dir); err != nil {
		t.Fatalf("OutputJSON returned error: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"init\n", "apply -auto-approve\n", "destroy -auto-approve\n", "output -json\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fake tofu log missing %q:\n%s", want, got)
		}
	}
}

// TestNewCLIHonorsExecutableEnv verifies explicit Terraform selection takes
// precedence over the default tofu-first lookup.
func TestNewCLIHonorsExecutableEnv(t *testing.T) {
	dir := t.TempDir()
	terraform := filepath.Join(dir, "terraform")
	if err := os.WriteFile(terraform, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tofu := filepath.Join(dir, "tofu")
	if err := os.WriteFile(tofu, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv(CommandEnvVar, "terraform")

	cli, err := NewCLI()
	if err != nil {
		t.Fatalf("NewCLI returned error: %v", err)
	}
	if cli.binary != terraform {
		t.Fatalf("NewCLI selected %q, want %q", cli.binary, terraform)
	}
}
