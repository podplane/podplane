// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/podplane/podplane/internal/config"
)

const validOIDCConfigJSON = `{
  "oidc": {
    "provider": { "kind": "aws", "region": "us-east-1", "account": "123456789012" },
    "hostname": "auth.example.com",
    "domain": { "zone": "example.com", "provider": { "kind": "aws" } },
    "connector": { "kind": "google", "client_secret_arn": "arn:connector" },
    "signing_key_secret_arn": "arn:signing",
    "clients": { "kubelogin": {} }
  }
}`

// TestOIDCCreateNoApplyGeneratesTerraform verifies OIDC create writes managed
// Terraform without invoking OpenTofu/Terraform.
func TestOIDCCreateNoApplyGeneratesTerraform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.oidc.jsonc")
	if err := os.WriteFile(path, []byte(validOIDCConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newOIDCCreateCmd(&config.Config{})
	cmd.SetArgs([]string{"--oidc-config", path, "--no-apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("oidc create --no-apply returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "podplane.oidc.tf")); err != nil {
		t.Fatalf("OIDC tf was not generated: %v", err)
	}
}

// TestOIDCDeleteNoApplyValidatesOnly verifies OIDC delete no-apply validates
// the config without invoking destroy dependencies.
func TestOIDCDeleteNoApplyValidatesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.oidc.jsonc")
	if err := os.WriteFile(path, []byte(validOIDCConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newOIDCDeleteCmd(&config.Config{})
	cmd.SetArgs([]string{"--oidc-config", path, "--no-apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("oidc delete --no-apply returned error: %v", err)
	}
}
