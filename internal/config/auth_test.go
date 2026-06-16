// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestLocalAuthUsesScopedKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(KeyringPassEnv, "test-password")
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	meta := AuthMetadata{Sub: "test-user", ClusterID: "default", Issuer: "https://oidc.localhost"}
	if err := c.AuthSet(meta, AuthSecrets{IDToken: "local-token"}, true); err != nil {
		t.Fatal(err)
	}
	got, secrets, err := c.AuthGet("test-user", "default", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sub != "test-user" || secrets.IDToken != "local-token" {
		t.Fatalf("AuthGet(local) = %#v, %#v", got, secrets)
	}
	got, secrets, err = c.AuthGet("test-user", "default", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sub != "" || secrets.IDToken != "" {
		t.Fatalf("AuthGet(remote) = %#v, %#v; want empty", got, secrets)
	}
}

func TestLocalAuthFallsBackToOldUnscopedKey(t *testing.T) {
	// TODO: remove this pre-1.0 when the legacy local auth fallback is removed.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(KeyringPassEnv, "test-password")
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	meta := AuthMetadata{Sub: "test-user", ClusterID: "default", Issuer: "https://oidc.localhost"}
	if err := c.AuthSet(meta, AuthSecrets{IDToken: "old-local-token"}, false); err != nil {
		t.Fatal(err)
	}
	got, secrets, err := c.AuthGet("test-user", "default", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sub != "test-user" || secrets.IDToken != "old-local-token" {
		t.Fatalf("AuthGet(local fallback) = %#v, %#v", got, secrets)
	}
}
