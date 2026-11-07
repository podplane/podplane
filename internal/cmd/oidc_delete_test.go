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

// validOIDCConfigJSON is a minimal valid AWS OIDC config.
const validOIDCConfigJSON = `{
  "oidc": {
    "provider": { "kind": "aws", "region": "us-east-1", "account": "123456789012" },
    "hostname": "auth.example.com",
    "domain": { "zone": "example.com", "provider": { "kind": "aws" } },
    "truster": {
      "secrets": { "signing_key_name": "arn:signing", "encryption_key_name": "arn:encryption" },
      "user_login_connectors": { "google": { "type": "google", "display_name": "Google", "credentials_secret": "arn:connector" } },
      "static_policy": { "default_redirect_uris": ["http://localhost:8000"], "clients": { "example-cluster": {} } }
    }
  }
}`

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
