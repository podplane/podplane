// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadOrCreateKeypair_GeneratesAndPersists checks that subsequent loads reuse the generated private key.
func TestLoadOrCreateKeypair_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oidc-key.pem")

	k1, err := LoadOrCreateKeypair(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if k1 == nil {
		t.Fatal("first call returned nil key")
	}

	k2, err := LoadOrCreateKeypair(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if k1.D.Cmp(k2.D) != 0 {
		t.Fatal("expected second call to load the same key")
	}
}

// TestHandler_ConstructsWithoutError checks handler creation with a generated signing key.
func TestHandler_ConstructsWithoutError(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreateKeypair(filepath.Join(dir, "k.pem"))
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	if _, err := Handler("http://localhost:1234/oidc", key, func(string) error { return nil }); err != nil {
		t.Fatalf("Handler: %v", err)
	}
}

// TestHandler_UsesValidatedClientIDAsAudience checks token audience selection and rejection of unknown clients.
func TestHandler_UsesValidatedClientIDAsAudience(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreateKeypair(filepath.Join(dir, "k.pem"))
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	h, err := Handler("http://localhost:1234/oidc", key, func(clientID string) error {
		if clientID != "default" {
			return fmt.Errorf("unknown local cluster")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader("grant_type=refresh_token&refresh_token=test&client_id=default"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("token status = %d, body = %s", res.Code, res.Body.String())
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	claims := decodeJWTPart(t, strings.Split(body.IDToken, ".")[1])
	if got := fmt.Sprint(claims["aud"]); got != "[default]" {
		t.Fatalf("aud = %s, want [default]", got)
	}

	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader("grant_type=refresh_token&refresh_token=test&client_id=other"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid client status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

// decodeJWTPart decodes a base64url JWT segment into a JSON object or fails the test.
func decodeJWTPart(t *testing.T, part string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		t.Fatalf("decode jwt part: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal jwt part: %v", err)
	}
	return out
}
