// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestExchange verifies the RFC 8693 request and Truster response contract.
func TestExchange(t *testing.T) {
	downstream := testJWT("trusted:subject")
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"grant_types_supported":[%q]}`, server.URL, server.URL+"/authorize", server.URL+"/token", tokenExchangeGrant)
		case "/token":
			_ = r.ParseForm()
			want := url.Values{
				"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "client_id": {"cluster"},
				"subject_token": {testJWT("upstream")}, "subject_token_type": {"urn:ietf:params:oauth:token-type:id_token"},
				"requested_token_type": {"urn:ietf:params:oauth:token-type:id_token"},
			}
			if r.Form.Encode() != want.Encode() {
				t.Errorf("exchange form = %v", r.Form)
			}
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"issued_token_type":"urn:ietf:params:oauth:token-type:id_token","token_type":"Bearer","expires_in":300}`, downstream)
		}
	}))
	defer server.Close()
	tokens, err := Exchange(context.Background(), server.Client(), server.URL, "cluster", testJWT("upstream"))
	if err != nil || tokens.IDToken != downstream {
		t.Fatalf("Exchange = %#v, %v", tokens, err)
	}
}

// TestExchangeReportsUnsupportedServer verifies discovery-based capability errors.
func TestExchangeReportsUnsupportedServer(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"grant_types_supported":["authorization_code"]}`, server.URL, server.URL+"/authorize", server.URL+"/token")
			return
		}
		t.Fatal("token endpoint should not be called")
	}))
	defer server.Close()

	_, err := Exchange(context.Background(), server.Client(), server.URL, "cluster", testJWT("upstream"))
	if err == nil || !strings.Contains(err.Error(), "OIDC server does not support service login") {
		t.Fatalf("Exchange error = %v", err)
	}
}

// TestExchangeReportsUnsupportedGrant verifies OAuth unsupported-grant errors.
func TestExchangeReportsUnsupportedGrant(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q}`, server.URL, server.URL+"/authorize", server.URL+"/token")
		case "/token":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
		}
	}))
	defer server.Close()

	_, err := Exchange(context.Background(), server.Client(), server.URL, "cluster", testJWT("upstream"))
	if err == nil || !strings.Contains(err.Error(), "OIDC server does not support service login") {
		t.Fatalf("Exchange error = %v", err)
	}
}

// testJWT builds a structurally valid JWT for OIDC tests.
func testJWT(sub string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":%q,"aud":"cluster"}`, sub)))
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return strings.Join([]string{header, payload, signature}, ".")
}
