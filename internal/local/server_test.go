// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLocalServicePeerAllowlistAllowsLoopback checks IPv4 and IPv6 loopback peers reach the handler.
func TestLocalServicePeerAllowlistAllowsLoopback(t *testing.T) {
	handler := localServicePeerAllowlist(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}))

	for _, remoteAddr := range []string{"127.0.0.1:12345", "127.42.0.1:12345", "[::1]:12345"} {
		t.Run(remoteAddr, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://localhost/deps/", nil)
			r.RemoteAddr = remoteAddr
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, r)

			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
			}
		})
	}
}

// TestLocalServicePeerAllowlistRejectsNonLoopback checks remote and malformed peer addresses receive forbidden responses.
func TestLocalServicePeerAllowlistRejectsNonLoopback(t *testing.T) {
	handler := localServicePeerAllowlist(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}))

	for _, remoteAddr := range []string{"10.0.2.15:12345", "192.168.1.10:12345", "[fd00::1]:12345", "not-an-addr"} {
		t.Run(remoteAddr, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://localhost/deps/", nil)
			r.RemoteAddr = remoteAddr
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, r)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
			}
		})
	}
}
