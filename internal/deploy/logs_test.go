// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"strings"
	"testing"
)

func TestLogsArgsUsesAppSelectorAndFollowsSelectedContainer(t *testing.T) {
	t.Parallel()
	got := logsArgs(LogsOptions{Name: "hello", Container: "hello-container"})
	want := []string{"logs", "--follow", "--container", "hello-container", "-l", "app.kubernetes.io/instance=hello"}
	assertStringSlicesEqual(t, got, want)
}

func TestLogsArgsCanFollowAllContainersWithPrefixes(t *testing.T) {
	t.Parallel()
	got := logsArgs(LogsOptions{Name: "hello", AllContainers: true})
	want := []string{"logs", "--follow", "--all-containers=true", "--prefix=true", "-l", "app.kubernetes.io/instance=hello"}
	assertStringSlicesEqual(t, got, want)
}

func TestLogsArgsIncludesKubeContextFlags(t *testing.T) {
	t.Parallel()
	got := logsArgs(LogsOptions{Name: "hello", Namespace: "apps", Context: "dev", Kubeconfig: "/tmp/kubeconfig", Container: "hello-container"})
	want := []string{"--context", "dev", "--kubeconfig", "/tmp/kubeconfig", "--namespace", "apps", "logs", "--follow", "--container", "hello-container", "-l", "app.kubernetes.io/instance=hello"}
	assertStringSlicesEqual(t, got, want)
}

func TestParseLogsPodInfoUsesDefaultContainerAnnotation(t *testing.T) {
	t.Parallel()
	info, err := parseLogsPodInfo("hello", []byte(`{"items":[{"metadata":{"annotations":{"kubectl.kubernetes.io/default-container":"hello-container"}},"spec":{"containers":[{"name":"hello-container"},{"name":"hello-caddy"}]}}]}`))
	if err != nil {
		t.Fatalf("parseLogsPodInfo error = %v", err)
	}
	if got, want := info.DefaultContainer, "hello-container"; got != want {
		t.Fatalf("DefaultContainer = %q, want %q", got, want)
	}
	assertStringSlicesEqual(t, info.Containers, []string{"hello-caddy", "hello-container"})
}

func TestParseLogsPodInfoIgnoresInvalidDefaultContainerAnnotation(t *testing.T) {
	t.Parallel()
	info, err := parseLogsPodInfo("hello", []byte(`{"items":[{"metadata":{"annotations":{"kubectl.kubernetes.io/default-container":"missing"}},"spec":{"containers":[{"name":"hello-container"},{"name":"hello-caddy"}]}}]}`))
	if err != nil {
		t.Fatalf("parseLogsPodInfo error = %v", err)
	}
	if info.DefaultContainer != "" {
		t.Fatalf("DefaultContainer = %q, want empty", info.DefaultContainer)
	}
	if got, want := info.AppContainer, "hello-container"; got != want {
		t.Fatalf("AppContainer = %q, want %q", got, want)
	}
}

func TestParseLogsPodInfoRecognizesWebContainerAsAppContainer(t *testing.T) {
	t.Parallel()
	info, err := parseLogsPodInfo("hello", []byte(`{"items":[{"spec":{"containers":[{"name":"caddy"},{"name":"web"}]}}]}`))
	if err != nil {
		t.Fatalf("parseLogsPodInfo error = %v", err)
	}
	if got, want := info.AppContainer, "web"; got != want {
		t.Fatalf("AppContainer = %q, want %q", got, want)
	}
}

func TestDefaultLogsContainerPromptsWhenAmbiguous(t *testing.T) {
	t.Parallel()
	selected := ""
	container, err := selectDefaultLogsContainer(logsContainerInfo{Containers: []string{"sidecar", "worker"}}, LogsOptions{
		Name: "hello",
		SelectContainer: func(containers []string) (string, bool, error) {
			assertStringSlicesEqual(t, containers, []string{"sidecar", "worker"})
			selected = "worker"
			return selected, true, nil
		},
	})
	if err != nil {
		t.Fatalf("selectDefaultLogsContainer error = %v", err)
	}
	if container != selected {
		t.Fatalf("container = %q, want %q", container, selected)
	}
}

func TestDefaultLogsContainerNonInteractiveErrorSuggestsFlags(t *testing.T) {
	t.Parallel()
	_, err := selectDefaultLogsContainer(logsContainerInfo{Containers: []string{"sidecar", "worker"}}, LogsOptions{Name: "hello"})
	if err == nil {
		t.Fatal("selectDefaultLogsContainer error = nil, want error")
	}
	msg := err.Error()
	for _, want := range []string{"--container <sidecar|worker>", "--all"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

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
