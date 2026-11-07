// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfstate

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGooglePreflightAuthentication exercises real ADC refresh failures and
// distinguishes reauthentication from a storage permission denial.
func TestGooglePreflightAuthentication(t *testing.T) {
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credentials, []byte(`{"type":"authorized_user","client_id":"test","client_secret":"test","refresh_token":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentials)
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, reauth := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.Host, "oauth") {
				if reauth {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"reauth related error (invalid_rapt)","error_subtype":"invalid_rapt"}`)
				} else {
					_, _ = io.WriteString(w, `{"access_token":"test","token_type":"Bearer","expires_in":3600}`)
				}
				return
			}
			if r.Method != http.MethodGet {
				t.Errorf("preflight attempted a mutation: %s", r.Method)
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":403,"message":"storage.buckets.get denied"}}`)
		}))
		dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		}
		transport := &http.Transport{DialContext: dial, DialTLSContext: dial}
		http.DefaultTransport = transport
		err := (Backend{Kind: "gcs", Bucket: "example-state", Project: "example"}).Preflight(t.Context())
		transport.CloseIdleConnections()
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "before Terraform generation") {
			t.Fatalf("preflight error = %v", err)
		}
		if got := strings.Contains(err.Error(), "gcloud auth application-default login"); got != reauth {
			t.Fatalf("reauth=%v, error = %v", reauth, err)
		}
	}
}
