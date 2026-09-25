// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/podplane/podplane/pkg/seeds"
)

// TestLocalStartRecommendedChecksOrderComponents verifies recommended local
// start waits for each component's prerequisites in bootstrap order.
func TestLocalStartRecommendedChecksOrderComponents(t *testing.T) {
	checks := LocalStartChecks(LocalStartOptions{SeedName: seeds.Recommended})
	wantOrder := []string{"cilium", "secrets-store-csi-driver", "podplane-operator", "envoy-gateway", "ingress"}
	if len(checks) != len(wantOrder) {
		t.Fatalf("recommended checks count = %d, want %d", len(checks), len(wantOrder))
	}
	byKey := map[string]Check{}
	for i, check := range checks {
		if check.Key != wantOrder[i] {
			t.Fatalf("recommended check %d = %q, want %q", i, check.Key, wantOrder[i])
		}
		byKey[check.Key] = check
	}

	secretsStore, ok := byKey["secrets-store-csi-driver"]
	if !ok {
		t.Fatal("recommended checks missing secrets-store-csi-driver")
	}
	if got, want := secretsStore.DependsOn, []string{"cilium"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("secrets-store-csi-driver dependencies = %v, want %v", got, want)
	}

	operator, ok := byKey["podplane-operator"]
	if !ok {
		t.Fatal("recommended checks missing podplane-operator")
	}
	if got, want := operator.DependsOn, []string{"secrets-store-csi-driver"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("podplane-operator dependencies = %v, want %v", got, want)
	}

	envoy, ok := byKey["envoy-gateway"]
	if !ok {
		t.Fatal("recommended checks missing envoy-gateway")
	}
	if envoy.Name != "envoy-gateway" {
		t.Fatalf("envoy-gateway name = %q, want envoy-gateway", envoy.Name)
	}
	if got, want := envoy.DependsOn, []string{"podplane-operator"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("envoy-gateway dependencies = %v, want %v", got, want)
	}

	ingress, ok := byKey["ingress"]
	if !ok {
		t.Fatal("recommended checks missing ingress")
	}
	if got, want := ingress.DependsOn, []string{"envoy-gateway"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("ingress dependencies = %v, want %v", got, want)
	}
	if ingress.Kind != "ingress" || !ingress.Required {
		t.Fatalf("ingress = %#v, want required ingress check", ingress)
	}
}

// TestCheckLocalIngressProxyAcceptsGatewayNotFound verifies route misses still
// prove the local ingress proxy reached the gateway.
func TestCheckLocalIngressProxyAcceptsGatewayNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	result := checkLocalIngressProxy(context.Background(), func() (string, error) { return server.URL, nil })
	if !result.Ready || result.Status != StatusReady {
		t.Fatalf("checkLocalIngressProxy = %#v, want ready", result)
	}
}

// TestCheckLocalIngressProxyDialsLocalhostOnLoopback verifies local ingress
// checks do not depend on host DNS resolving wildcard .localhost names.
func TestCheckLocalIngressProxyDialsLocalhostOnLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	url := strings.Replace(server.URL, "127.0.0.1", "default.localhost", 1)
	result := checkLocalIngressProxy(context.Background(), func() (string, error) { return url, nil })
	if !result.Ready || result.Status != StatusReady {
		t.Fatalf("checkLocalIngressProxy = %#v, want ready", result)
	}
}

// TestCheckLocalIngressProxyWaitsOnGatewayErrors verifies local proxy upstream
// failures are not mistaken for a healthy gateway connection.
func TestCheckLocalIngressProxyWaitsOnGatewayErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "upstream unavailable", status)
		}))

		result := checkLocalIngressProxy(context.Background(), func() (string, error) { return server.URL, nil })
		server.Close()
		if result.Ready || result.Status != StatusPending {
			t.Fatalf("checkLocalIngressProxy status %d = %#v, want pending", status, result)
		}
	}
}
