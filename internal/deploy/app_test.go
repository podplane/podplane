// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"strings"
	"testing"
)

// TestParseAppInfoUsesDefaultContainerAnnotation verifies annotation-based container defaults.
func TestParseAppInfoUsesDefaultContainerAnnotation(t *testing.T) {
	t.Parallel()
	info, err := parseAppInfo("hello", []byte(`{"items":[{"metadata":{"name":"hello-b","annotations":{"kubectl.kubernetes.io/default-container":"hello-container"}},"spec":{"containers":[{"name":"hello-container"},{"name":"hello-caddy"}]},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`))
	if err != nil {
		t.Fatalf("parseAppInfo error = %v", err)
	}
	if got, want := info.DefaultContainer, "hello-container"; got != want {
		t.Fatalf("DefaultContainer = %q, want %q", got, want)
	}
	assertStringSlicesEqual(t, info.Containers, []string{"hello-caddy", "hello-container"})
	if got, want := info.Pods[0].Name, "hello-b"; got != want {
		t.Fatalf("pod name = %q, want %q", got, want)
	}
	if !info.Pods[0].Ready {
		t.Fatal("pod Ready = false, want true")
	}
}

// TestParseAppInfoIgnoresInvalidDefaultContainerAnnotation verifies stale annotations are ignored.
func TestParseAppInfoIgnoresInvalidDefaultContainerAnnotation(t *testing.T) {
	t.Parallel()
	info, err := parseAppInfo("hello", []byte(`{"items":[{"metadata":{"annotations":{"kubectl.kubernetes.io/default-container":"missing"}},"spec":{"containers":[{"name":"hello-container"},{"name":"hello-caddy"}]}}]}`))
	if err != nil {
		t.Fatalf("parseAppInfo error = %v", err)
	}
	if info.DefaultContainer != "" {
		t.Fatalf("DefaultContainer = %q, want empty", info.DefaultContainer)
	}
	if got, want := info.AppContainer, "hello-container"; got != want {
		t.Fatalf("AppContainer = %q, want %q", got, want)
	}
}

// TestParseAppInfoRecognizesWebContainerAsAppContainer verifies preferred app names.
func TestParseAppInfoRecognizesWebContainerAsAppContainer(t *testing.T) {
	t.Parallel()
	info, err := parseAppInfo("hello", []byte(`{"items":[{"spec":{"containers":[{"name":"caddy"},{"name":"web"}]}}]}`))
	if err != nil {
		t.Fatalf("parseAppInfo error = %v", err)
	}
	if got, want := info.AppContainer, "web"; got != want {
		t.Fatalf("AppContainer = %q, want %q", got, want)
	}
}

// TestSelectDefaultAppPodPrefersReadyPod verifies pod selection uses Ready pods.
func TestSelectDefaultAppPodPrefersReadyPod(t *testing.T) {
	t.Parallel()
	pod, err := selectDefaultAppPod(appInfo{Pods: []appPodInfo{{Name: "hello-a"}, {Name: "hello-b", Ready: true}}}, AppTargetOptions{Name: "hello"})
	if err != nil {
		t.Fatalf("selectDefaultAppPod error = %v", err)
	}
	if got, want := pod, "hello-b"; got != want {
		t.Fatalf("pod = %q, want %q", got, want)
	}
}

// TestSelectDefaultAppContainerPromptsWhenAmbiguous verifies interactive selection.
func TestSelectDefaultAppContainerPromptsWhenAmbiguous(t *testing.T) {
	t.Parallel()
	selected := ""
	container, err := selectDefaultAppContainer(appInfo{Containers: []string{"sidecar", "worker"}}, AppTargetOptions{
		Name: "hello",
		SelectContainer: func(containers []string) (string, bool, error) {
			assertStringSlicesEqual(t, containers, []string{"sidecar", "worker"})
			selected = "worker"
			return selected, true, nil
		},
	}, "logs", true)
	if err != nil {
		t.Fatalf("selectDefaultAppContainer error = %v", err)
	}
	if container != selected {
		t.Fatalf("container = %q, want %q", container, selected)
	}
}

// TestSelectDefaultAppContainerNonInteractiveErrorSuggestsFlags verifies actionable errors.
func TestSelectDefaultAppContainerNonInteractiveErrorSuggestsFlags(t *testing.T) {
	t.Parallel()
	_, err := selectDefaultAppContainer(appInfo{Containers: []string{"sidecar", "worker"}}, AppTargetOptions{Name: "hello"}, "logs", true)
	if err == nil {
		t.Fatal("selectDefaultAppContainer error = nil, want error")
	}
	msg := err.Error()
	for _, want := range []string{"--container <sidecar|worker>", "--all"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}
