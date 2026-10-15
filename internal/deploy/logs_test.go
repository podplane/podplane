// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"testing"
)

// TestLogsArgsUsesAppSelectorAndFollowsSelectedContainer checks selection and follow arguments for one container.
func TestLogsArgsUsesAppSelectorAndFollowsSelectedContainer(t *testing.T) {
	t.Parallel()
	got := logsArgs(LogsOptions{Name: "hello", Container: "hello-container"})
	want := []string{"logs", "--follow", "--container", "hello-container", "-l", "app.kubernetes.io/instance=hello"}
	assertStringSlicesEqual(t, got, want)
}

// TestLogsArgsCanFollowAllContainersWithPrefixes checks prefixed output when following all containers.
func TestLogsArgsCanFollowAllContainersWithPrefixes(t *testing.T) {
	t.Parallel()
	got := logsArgs(LogsOptions{Name: "hello", AllContainers: true})
	want := []string{"logs", "--follow", "--all-containers=true", "--prefix=true", "-l", "app.kubernetes.io/instance=hello"}
	assertStringSlicesEqual(t, got, want)
}

// TestLogsArgsIncludesKubeContextFlags checks context, kubeconfig, and namespace argument placement.
func TestLogsArgsIncludesKubeContextFlags(t *testing.T) {
	t.Parallel()
	got := logsArgs(LogsOptions{Name: "hello", Namespace: "apps", Context: "dev", Kubeconfig: "/tmp/kubeconfig", Container: "hello-container"})
	want := []string{"--context", "dev", "--kubeconfig", "/tmp/kubeconfig", "--namespace", "apps", "logs", "--follow", "--container", "hello-container", "-l", "app.kubernetes.io/instance=hello"}
	assertStringSlicesEqual(t, got, want)
}

// assertStringSlicesEqual fails the test when ordered command arguments differ.
func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: got %#v want %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q: got %#v want %#v", i, got[i], want[i], got, want)
		}
	}
}
