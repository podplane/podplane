// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestSelectSource verifies explicit and detected identity-source selection.
func TestSelectSource(t *testing.T) {
	env := map[string]string{"GITHUB_ACTIONS": "true", "ACTIONS_ID_TOKEN_REQUEST_URL": "url", "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "token", "CI": "true"}
	getenv := func(k string) string { return env[k] }
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	source, err := SelectSource(SourceOptions{Environ: getenv, LookPath: missing})
	if err != nil || source.IdentityProvider != ProviderGitHub {
		t.Fatalf("SelectSource = %#v, %v", source, err)
	}
	source, err = SelectSource(SourceOptions{IdentityProvider: "none", Environ: getenv, LookPath: missing})
	if err != nil || !source.IsUserLogin() {
		t.Fatalf("--identity-provider=none = %#v, %v", source, err)
	}
	if _, err := SelectSource(SourceOptions{IdentityProvider: "buildkite", IdentityFile: "token.jwt"}); err == nil {
		t.Fatal("expected conflicting controls to fail")
	}
	delete(env, "ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	source, err = SelectSource(SourceOptions{Environ: getenv, LookPath: missing})
	if err != nil || !source.IsUserLogin() {
		t.Fatalf("incomplete GitHub environment = %#v, %v", source, err)
	}
}

// TestAcquireGitHubAndTokenFileRotation verifies provider acquisition and identity-file rereads.
func TestAcquireGitHubAndTokenFileRotation(t *testing.T) {
	token := testJWT("first")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") != "cluster" || r.Header.Get("Authorization") != "Bearer request-secret" {
			t.Errorf("request auth/audience incorrect")
		}
		_, _ = fmt.Fprintf(w, `{"value":%q}`, token)
	}))
	defer server.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", server.URL+"?existing=yes")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-secret")
	got, err := Acquire(context.Background(), server.Client(), Source{IdentityProvider: ProviderGitHub}, "cluster")
	if err != nil || got != token {
		t.Fatalf("Acquire(GitHub) = %q, %v", got, err)
	}

	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	source := Source{IdentityFile: path}
	if _, err := Acquire(context.Background(), http.DefaultClient, source, "cluster"); err != nil {
		t.Fatal(err)
	}
	rotated := testJWT("second")
	if err := os.WriteFile(path, []byte(rotated), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Acquire(context.Background(), http.DefaultClient, source, "cluster")
	if err != nil || got != rotated {
		t.Fatalf("rotated Acquire = %q, %v", got, err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), http.DefaultClient, source, "cluster"); err == nil {
		t.Fatal("expected unsafe permissions to fail")
	}
}
