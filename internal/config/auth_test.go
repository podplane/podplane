// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"strings"
	"testing"
)

// TestLocalAndRemoteAuthRemainDistinct verifies locality selects separate auth entries.
func TestLocalAndRemoteAuthRemainDistinct(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(KeyringPassEnv, "test-password")
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}

	meta := AuthMetadata{Sub: "test-user", ClusterID: "default", Issuer: "https://oidc.localhost"}
	localRef := AuthRef{Subject: meta.Sub, ClusterID: meta.ClusterID, Local: true}
	remoteRef := AuthRef{Subject: meta.Sub, ClusterID: meta.ClusterID}
	if err := c.AuthSet(meta, AuthSecrets{IDToken: "local-token"}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.AuthSet(meta, AuthSecrets{IDToken: "remote-token"}, false); err != nil {
		t.Fatal(err)
	}
	got, secrets, err := c.AuthGet(localRef)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sub != "test-user" || secrets.IDToken != "local-token" {
		t.Fatalf("AuthGet(local) = %#v, %#v", got, secrets)
	}
	got, secrets, err = c.AuthGet(remoteRef)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sub != "test-user" || secrets.IDToken != "remote-token" {
		t.Fatalf("AuthGet(remote) = %#v, %#v", got, secrets)
	}
	if err := c.AuthDelete(meta.Sub, meta.ClusterID, true); err != nil {
		t.Fatal(err)
	}
	_, secrets, err = c.AuthGet(remoteRef)
	if err != nil || secrets.IDToken != "remote-token" {
		t.Fatalf("remote auth after local delete = %#v, %v", secrets, err)
	}
}

// TestAuthOpaqueKeysPreserveSubjects verifies complex trusted subjects remain distinct.
func TestAuthOpaqueKeysPreserveSubjects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(KeyringPassEnv, "test-password")
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	subs := []string{`trusted:Org/Repo.Ref:"A\\B"`, `trusted:org/repo.ref:"a\\b"`}
	for i, sub := range subs {
		meta := AuthMetadata{Sub: sub, ClusterID: "same"}
		ref := AuthRef{Subject: sub, ClusterID: meta.ClusterID}
		if err := c.AuthSet(meta, AuthSecrets{IDToken: "token"}, false); err != nil {
			t.Fatal(err)
		}
		got, _, err := c.AuthGet(ref)
		if err != nil || got.Sub != sub {
			t.Fatalf("subject %d = %q, %v", i, got.Sub, err)
		}
	}
	entries, err := c.AuthListByCluster("same", false)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %#v, %v", entries, err)
	}
	if err := c.AuthDelete(subs[0], "same", false); err != nil {
		t.Fatal(err)
	}
	entries, _ = c.AuthListByCluster("same", false)
	if len(entries) != 1 || entries[0].Sub != subs[1] {
		t.Fatalf("after delete = %#v", entries)
	}
}

// TestAuthStoresRenewalMetadataWithoutTokens verifies metadata and secrets use separate stores.
func TestAuthStoresRenewalMetadataWithoutTokens(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(KeyringPassEnv, "test-password")
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	meta := AuthMetadata{
		Sub: "trusted:github:example", ClusterID: "cluster", Issuer: "https://issuer.example",
		ClientID: "client", IdentityProvider: "github", OIDCCAPath: "/tmp/oidc-ca.pem",
	}
	secrets := AuthSecrets{IDToken: "service-token-secret", RefreshToken: "refresh-token-secret"}
	ref := AuthRef{Subject: meta.Sub, ClusterID: meta.ClusterID}
	if err := c.AuthSet(meta, secrets, false); err != nil {
		t.Fatal(err)
	}
	gotMeta, gotSecrets, err := c.AuthGet(ref)
	if err != nil {
		t.Fatal(err)
	}
	if gotMeta.IdentityProvider != "github" || gotMeta.OIDCCAPath != meta.OIDCCAPath || gotSecrets != secrets {
		t.Fatalf("AuthGet = %#v, %#v", gotMeta, gotSecrets)
	}
	data, err := os.ReadFile(c.File())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secrets.IDToken) || strings.Contains(string(data), secrets.RefreshToken) {
		t.Fatal("auth config contains token material")
	}
}
