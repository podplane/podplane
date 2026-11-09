// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCLIInvokesTofuWithExpectedArguments verifies the CLI executor uses a tofu
// executable found on PATH and passes expected init/destroy/output arguments.
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
	if err := cli.Init(ctx, dir, "backend.tfbackend"); err != nil {
		t.Fatalf("Init returned error: %v", err)
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
	for _, want := range []string{"init -backend-config=backend.tfbackend\n", "destroy -auto-approve\n", "output -json\n"} {
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

// TestCLIPlatformAndConfigFile verifies platform detection and shared CLI configuration wiring.
func TestCLIPlatformAndConfigFile(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tofu")
	logPath := filepath.Join(dir, "config.log")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf '{\"platform\":\"linux_arm64\"}'; exit; fi\nprintf '%s' \"$TF_CLI_CONFIG_FILE\" > " + logPath + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv(CommandEnvVar, "")
	cli, err := NewCLI()
	if err != nil {
		t.Fatal(err)
	}
	platform, err := cli.Platform(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if platform != "linux_arm64" {
		t.Fatalf("Platform = %q", platform)
	}
	configPath := filepath.Join(dir, "terraform.tfrc")
	if err := cli.WithCLIConfig(configPath).Init(context.Background(), dir, "backend.tfbackend"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != configPath {
		t.Fatalf("TF_CLI_CONFIG_FILE = %q", raw)
	}
}

// TestCLIDevelopmentProvider verifies explicit local selection survives the
// managed mirror config, preserves unrelated settings and never persists there.
func TestCLIDevelopmentProvider(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "terraform-provider-podplane")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(dir, "tofu")
	if err := os.WriteFile(engine, []byte("#!/bin/sh\nprintf '%s\\n' \"$TF_CLI_CONFIG_FILE\"\n/bin/cat \"$TF_CLI_CONFIG_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(CommandEnvVar, engine)
	t.Setenv(ProviderDirEnvVar, dir)
	t.Setenv("TF_CLI_CONFIG_FILE", filepath.Join(dir, "must-not-be-read"))
	base := `credentials "example.test" { token = "test-only" }
provider_installation {
  # Braces in strings/comments must not affect the insertion point: }
  dev_overrides { "registry.terraform.io/podplane/podplane" = "/old/{provider}" }
  dev_overrides { "example.test/acme/other" = "/other" }
  filesystem_mirror { path = "/shared/mirror" }
}
`
	path := filepath.Join(dir, "managed.tfrc")
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	cli, err := NewCLI()
	if err != nil {
		t.Fatal(err)
	}
	cli = cli.WithCLIConfig(path)
	out, err := cli.OutputJSON(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(out), "\n", 2)
	if len(parts) != 2 || parts[0] == path {
		t.Fatalf("missing private configuration: %s", out)
	}
	if _, err := os.Stat(parts[0]); !os.IsNotExist(err) {
		t.Fatalf("private config was not removed: %v", err)
	}
	config := parts[1]
	for _, want := range []string{`token = "test-only"`, `"example.test/acme/other" = "/other"`, `path = "/shared/mirror"`, `"registry.opentofu.org/podplane/podplane" = "` + dir + `"`} {
		if !strings.Contains(config, want) {
			t.Fatalf("development config missing %q: %s", want, config)
		}
	}
	local := strings.Index(config, `"registry.terraform.io/podplane/podplane" = "`+dir+`"`)
	if local < strings.Index(config, `"example.test/acme/other"`) || local > strings.Index(config, "filesystem_mirror") {
		t.Fatalf("override not between existing overrides and mirror: %s", config)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != base {
		t.Fatalf("managed config changed: %s, %v", raw, err)
	}
	t.Setenv(ProviderDirEnvVar, "")
	out, err = cli.OutputJSON(context.Background(), dir)
	if err != nil || string(out) != path+"\n"+base {
		t.Fatalf("override leaked into ordinary run: %s, %v", out, err)
	}
	t.Setenv(ProviderDirEnvVar, filepath.Join(dir, "missing"))
	if _, err := cli.OutputJSON(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "make build") {
		t.Fatalf("missing provider silently fell back to registry: %v", err)
	}
}

// TestCLIWaitsForShutdown sends real signals to a separate CLI process, checking
// graceful cleanup, repeated Ctrl-C, and forced cleanup of an unresponsive tree.
func TestCLIWaitsForShutdown(t *testing.T) {
	for _, mode := range []string{"interrupt", "terminate", "forced"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			engine := filepath.Join(dir, "tofu")
			trap := `trap 'echo stopping > "$TEST_PROCESS_DIR/stopping"; /bin/sleep 0.2; kill "$child"; wait "$child"; echo cleaned > "$TEST_PROCESS_DIR/cleaned"; exit 0' INT TERM`
			if mode == "forced" {
				trap = `trap '' INT TERM`
			}
			script := "#!/bin/sh\n" + trap + `
/bin/sleep 120 &
child=$!
echo "$$ $child" > "$TEST_PROCESS_DIR/ready"
while :; do wait "$child"; done
`
			if err := os.WriteFile(engine, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			supervisor := exec.Command(os.Args[0], "-test.run=^TestCLIProcessHelper$")
			supervisor.Env = append(os.Environ(), "TEST_PROCESS_HELPER=1", "TEST_PROCESS_DIR="+dir, CommandEnvVar+"="+engine)
			if mode == "interrupt" {
				supervisor.Env = append(supervisor.Env, "TEST_PROCESS_STREAM=1")
			}
			supervisor.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := supervisor.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-supervisor.Process.Pid, syscall.SIGKILL) })
			done := make(chan error, 1)
			go func() { done <- supervisor.Wait() }()
			ready := waitForProcessFile(t, filepath.Join(dir, "ready"))
			pids := strings.Fields(ready)
			if len(pids) != 2 {
				t.Fatalf("engine PIDs = %q", ready)
			}
			enginePID, _ := strconv.Atoi(pids[0])
			t.Cleanup(func() { _ = syscall.Kill(-enginePID, syscall.SIGKILL) })
			sig := syscall.SIGINT
			if mode == "terminate" {
				sig = syscall.SIGTERM
			}
			if err := syscall.Kill(-supervisor.Process.Pid, sig); err != nil {
				t.Fatal(err)
			}
			if mode == "interrupt" {
				waitForProcessFile(t, filepath.Join(dir, "stopping"))
				// A second Ctrl-C must not kill the supervisor before cleanup.
				_ = syscall.Kill(-supervisor.Process.Pid, sig)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("CLI exited before orderly shutdown: %v", err)
				}
			case <-time.After(40 * time.Second):
				t.Fatal("CLI failed to finish shutdown")
			}
			if mode != "forced" {
				if _, err := os.Stat(filepath.Join(dir, "cleaned")); err != nil {
					t.Fatalf("CLI returned before engine cleanup: %v", err)
				}
			}
			for _, value := range pids {
				// An orphan zombie may await init's reap on Linux, but must not
				// be running. The direct engine is reaped by the CLI itself.
				out, _ := exec.Command("ps", "-o", "stat=", "-p", value).Output()
				if state := strings.TrimSpace(string(out)); state != "" && !strings.HasPrefix(state, "Z") {
					t.Errorf("process %s still alive after CLI exit: %s", value, state)
				}
			}
		})
	}
}

// TestCLIProcessHelper is the isolated parent receiving the test's OS signals.
func TestCLIProcessHelper(t *testing.T) {
	if os.Getenv("TEST_PROCESS_HELPER") != "1" {
		return
	}
	cli, err := NewCLI()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if os.Getenv("TEST_PROCESS_STREAM") == "1" {
		err = cli.Destroy(context.Background(), os.Getenv("TEST_PROCESS_DIR"), false)
	} else {
		_, err = cli.OutputJSON(context.Background(), os.Getenv("TEST_PROCESS_DIR"))
	}
	if !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "expected cancellation, got", err)
		os.Exit(2)
	}
}

// waitForProcessFile waits for a child readiness marker, bounded on failures.
func waitForProcessFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return string(raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child did not create %s", path)
	return ""
}
