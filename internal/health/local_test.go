// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
