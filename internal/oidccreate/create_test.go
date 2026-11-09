// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidccreate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/oidcconfig"
)

// TestRunNoApplyGeneratesStack verifies that Run writes the generated stack
// and its remote-state backend config for an existing config without
// contacting the cloud, and returns the issuer URL.
func TestRunNoApplyGeneratesStack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.oidc.jsonc")
	config := `{
  "oidc": {
    "provider": { "kind": "google", "project": "my-project", "region": "us-central1", "zone": "us-central1-a" },
    "hostname": "auth.example.com",
    "domain": { "zone": "example.com", "provider": { "kind": "google", "hosted_zone_name": "example-com" } },
    "truster": {
      "secrets": { "signing_key_name": "projects/my-project/secrets/signing/versions/latest" },
      "user_login_connectors": { "google": { "type": "google", "display_name": "Google", "credentials_secret": "projects/my-project/secrets/google/versions/latest" } },
      "static_policy": { "clients": { "prod-1": {} } }
    }
  }
}`
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	// Invalid explicit credentials must stop apply before writing Terraform,
	// but must not prevent the offline generation path below.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "missing-credentials.json"))
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	if _, err := Run(context.Background(), Options{ConfigPath: path}); err == nil || !strings.Contains(err.Error(), "before Terraform generation") {
		t.Fatalf("apply without credentials = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "podplane.oidc.main.tf")); !os.IsNotExist(err) {
		t.Fatalf("Terraform generated before checking credentials: %v", err)
	}
	// A caller's cluster provider must never replace an existing server's
	// explicitly configured Google project/provider.
	issuerURL, err := Run(context.Background(), Options{
		ConfigPath: path, NoApply: true,
		Provider: &oidcconfig.Provider{Kind: "aws", Region: "eu-west-2", Account: "987654321098", Profile: "production"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if issuerURL != "https://auth.example.com" {
		t.Fatalf("issuer URL = %q, want https://auth.example.com", issuerURL)
	}
	for _, name := range []string{
		".podplane/oidc-schema.json",
		"podplane.oidc.main.tf",
		"podplane.oidc.variables.tf",
		"podplane.oidc.outputs.tf",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("OIDC file %s was not generated: %v", name, err)
		}
	}
	backend, err := os.ReadFile(filepath.Join(dir, ".podplane/oidc-backend.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backend), `bucket = "auth-example-com-my-project-tfstate-c204c854ffe5f35a"`) {
		t.Fatalf("backend config = %s, want the OIDC state bucket", backend)
	}
}
