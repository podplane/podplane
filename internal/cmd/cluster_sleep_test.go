// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/config"
)

// TestClusterSleepInterface checks registration, context flags, force approval, and rejection of positional arguments.
func TestClusterSleepInterface(t *testing.T) {
	cmd := newClusterSleepCmd(&config.Config{})
	for _, name := range []string{"context", "kubeconfig", "force", "auto-approve"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s", name)
		}
	}
	if cmd.Flags().Lookup("cluster-config") != nil {
		t.Fatal("sleep must not require a cluster config")
	}
	if cmd.Flags().Lookup("auto-approve").Shorthand != "y" {
		t.Fatal("missing -y")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Fatal("accepted positional argument")
	}
	if found, _, err := newClusterCmd(&config.Config{}).Find([]string{"sleep"}); err != nil || found.Name() != "sleep" {
		t.Fatalf("sleep not registered: %v", err)
	}
}

// TestClusterSleepContextAndNoninteractiveForce checks selection is preserved and noninteractive force requires explicit approval.
func TestClusterSleepContextAndNoninteractiveForce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("CMD_SLEEP_LOG", log)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$CMD_SLEEP_LOG"
shift 4
case "$1" in
config) echo '{"contexts":[{"name":"alias","context":{"cluster":"podplane-test-cluster"}}]}' ;;
get) echo '{"items":[{"metadata":{"name":"test-cluster--tenant","namespace":"custom-ns"},"spec":{"infrastructureRef":{"kind":"NstanceCluster","name":"test-cluster--tenant"}}}]}' ;;
create) cat >/dev/null; echo '{"status":{"allowed":true}}' ;;
patch) ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A pipe makes the force prompt deterministically noninteractive even if
	// the tests are started in a terminal. Piped "yes" is not implicit approval.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString("yes\n")
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
	for _, approve := range []bool{false, true} {
		cmd := newClusterSleepCmd(&config.Config{})
		args := []string{"--context", "alias", "--kubeconfig", "/selected/config", "--force"}
		if approve {
			args = append(args, "-y")
		}
		cmd.SetArgs(args)
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		err := cmd.Execute()
		if approve && err != nil {
			t.Fatal(err)
		}
		if !approve && (err == nil || !strings.Contains(err.Error(), "noninteractive")) {
			t.Fatalf("expected safe refusal, got %v", err)
		}
		if !strings.Contains(output.String(), "active workloads and connections may be interrupted") {
			t.Fatalf("no force warning: %s", output.String())
		}
		if approve && !strings.Contains(output.String(), "not waiting for completion") {
			t.Fatalf("no submission result: %s", output.String())
		}
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "patch clusters") != 1 {
		t.Fatalf("unexpected patches: %s", calls)
	}
	for _, call := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if !strings.HasPrefix(call, "--context alias --kubeconfig /selected/config ") {
			t.Fatalf("selection lost: %s", call)
		}
	}
}
