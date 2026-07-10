// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"testing"
	"time"
)

// TestShellArgsUsesSelectedContainerWithTTY verifies interactive exec args.
func TestShellArgsUsesSelectedContainerWithTTY(t *testing.T) {
	t.Parallel()
	got := shellArgs(AppTargetOptions{Name: "hello"}, "hello-pod", "web", true, []string{"bash", "-l"})
	want := []string{"exec", "-i", "-t", "hello-pod", "--container", "web", "--", "bash", "-l"}
	assertStringSlicesEqual(t, got, want)
}

// TestShellArgsPreservesOneOffCommandArgv verifies one-off commands are not shell-joined.
func TestShellArgsPreservesOneOffCommandArgv(t *testing.T) {
	t.Parallel()
	got := shellArgs(AppTargetOptions{Name: "hello", Namespace: "apps", Context: "dev", Kubeconfig: "/tmp/kubeconfig"}, "hello-pod", "web", false, []string{"npm", "run", "migrate"})
	want := []string{"--context", "dev", "--kubeconfig", "/tmp/kubeconfig", "--namespace", "apps", "exec", "-i", "hello-pod", "--container", "web", "--", "npm", "run", "migrate"}
	assertStringSlicesEqual(t, got, want)
}

// TestShellPromptCommandPrependsEnv verifies prompt exports become env args.
func TestShellPromptCommandPrependsEnv(t *testing.T) {
	t.Parallel()
	got := shellPromptCommand(map[string]string{"ZED": "last", "FOO": "bar"}, []string{"printenv", "FOO"})
	want := []string{"env", "FOO=bar", "ZED=last", "printenv", "FOO"}
	assertStringSlicesEqual(t, got, want)
}

// TestDebugArgsTargetsOriginalContainer verifies kubectl debug args.
func TestDebugArgsTargetsOriginalContainer(t *testing.T) {
	t.Parallel()
	got := debugArgs(AppTargetOptions{Name: "hello", Namespace: "apps", Context: "dev", Kubeconfig: "/tmp/kubeconfig"}, "hello-pod", "hello-container", "podplane-debug-1", "busybox:latest")
	want := []string{"--context", "dev", "--kubeconfig", "/tmp/kubeconfig", "--namespace", "apps", "debug", "hello-pod", "-i", "-t", "--quiet", "--profile", "general", "--image", "busybox:latest", "--target", "hello-container", "--container", "podplane-debug-1", "--", "sh"}
	assertStringSlicesEqual(t, got, want)
}

// TestMarkDebugPodArgsMarksPodForCleanup verifies debug pod annotations.
func TestMarkDebugPodArgsMarksPodForCleanup(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC)
	got := markDebugPodArgs(AppTargetOptions{Name: "hello", Namespace: "apps"}, "hello-pod", "dbg-1", "podplane-dbg-1", "hello-container", createdAt)
	want := []string{"--namespace", "apps", "annotate", "pod", "hello-pod", `podplane.dev/shell-debug-dbg-1={"container":"podplane-dbg-1","target":"hello-container","createdAt":"2026-07-10T04:00:00Z"}`, "--overwrite"}
	assertStringSlicesEqual(t, got, want)
}

// TestLabelDebugPodArgsAddsFilterLabel verifies debug pod labels.
func TestLabelDebugPodArgsAddsFilterLabel(t *testing.T) {
	t.Parallel()
	got := labelDebugPodArgs(AppTargetOptions{Name: "hello", Namespace: "apps"}, "hello-pod")
	want := []string{"--namespace", "apps", "label", "pod", "hello-pod", "podplane.dev/shell-debug=true", "--overwrite"}
	assertStringSlicesEqual(t, got, want)
}

// TestControllerOwnerReturnsController verifies owner selection.
func TestControllerOwnerReturnsController(t *testing.T) {
	t.Parallel()
	controller := true
	owner := controllerOwner([]ownerRef{{Kind: "ReplicaSet", Name: "rs", Controller: &controller}})
	if owner.Kind != "ReplicaSet" || owner.Name != "rs" {
		t.Fatalf("owner = %#v, want ReplicaSet/rs", owner)
	}
}
