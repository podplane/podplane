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

// TestLocalStartRecommendedChecksIncludeTrustPath verifies recommended local
// start waits for app trust material before reporting deploy-ready health.
func TestLocalStartRecommendedChecksIncludeTrustPath(t *testing.T) {
	checks := LocalStartChecks(LocalStartOptions{SeedName: seeds.Recommended})
	byKey := map[string]Check{}
	for _, check := range checks {
		byKey[check.Key] = check
	}

	trustManager, ok := byKey["trust-manager"]
	if !ok {
		t.Fatal("recommended checks missing trust-manager")
	}
	if got, want := trustManager.DependsOn, []string{"cert-manager-admission"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("trust-manager dependencies = %v, want %v", got, want)
	}

	bundle, ok := byKey["default-app-trust-bundle"]
	if !ok {
		t.Fatal("recommended checks missing default app trust bundle")
	}
	if got, want := bundle.DependsOn, []string{"trust-manager"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("default app trust bundle dependencies = %v, want %v", got, want)
	}
	if bundle.Kind != "configmap" || !bundle.Required {
		t.Fatalf("default app trust bundle = %#v, want required configmap", bundle)
	}
}

// TestCheckLocalIngressProxyAcceptsTraefikNotFound verifies Traefik route
// misses still prove the local ingress proxy reached Traefik.
func TestCheckLocalIngressProxyAcceptsTraefikNotFound(t *testing.T) {
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
// failures are not mistaken for a healthy Traefik connection.
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
