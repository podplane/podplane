// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadValidAWSConfig checks loading an AWS server configuration containing JSONC comments.
func TestLoadValidAWSConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podplane.oidc.jsonc")
	if err := os.WriteFile(path, []byte(`{
  // comments are allowed
  "oidc": {
    "provider": { "kind": "aws", "region": "us-east-1", "account": "123456789012", "profile": "default" },
    "hostname": "auth.example.com",
    "domain": { "zone": "example.com", "provider": { "kind": "aws" } },
    "truster": {
      "secrets": {
        "signing_key_name": "arn:aws:secretsmanager:us-east-1:123456789012:secret:signing",
        "encryption_key_name": "arn:aws:secretsmanager:us-east-1:123456789012:secret:encryption"
      },
      "user_login_connectors": {
        "email": { "type": "email", "display_name": "Email" }
      },
      "static_policy": {
        "default_redirect_uris": ["http://localhost:8000"],
        "clients": { "example-cluster": { "user_group_mapping": "prod" } },
        "user_group_mappings": { "prod": { "ops@example.com": ["admins"] } }
      }
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.OIDC.Provider.Kind != "aws" {
		t.Fatalf("provider kind = %q, want aws", cfg.OIDC.Provider.Kind)
	}
	connector := cfg.OIDC.Truster["user_login_connectors"].(map[string]any)["email"].(map[string]any)
	if connector["type"] != "email" {
		t.Fatalf("truster email connector = %#v, want it loaded unchanged", connector)
	}
}

// TestValidateGoogleProvider checks that a complete Google Cloud config is
// accepted and that each Google-specific requirement is enforced.
func TestValidateGoogleProvider(t *testing.T) {
	if err := Validate(testDraft(t, "google")); err != nil {
		t.Fatalf("Validate(google draft) = %v, want nil", err)
	}
	for name, tc := range map[string]struct {
		mutate func(*OIDC)
		want   string
	}{
		"missing zone":         {func(o *OIDC) { o.Provider.Zone = "" }, "zone are required"},
		"zone outside region":  {func(o *OIDC) { o.Provider.Zone = "us-east1-b" }, "is not in region"},
		"missing managed zone": {func(o *OIDC) { o.Domain.Provider.HostedZoneName = "" }, "hosted_zone_name is required"},
		"mismatched DNS cloud": {func(o *OIDC) { o.Domain.Provider.Kind = "aws" }, "must match"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testDraft(t, "google")
			tc.mutate(&cfg.OIDC)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

// TestValidateRejectsDeploymentOwnedTrusterSettings checks that settings
// Podplane derives from the deployment cannot also be set in oidc.truster.
func TestValidateRejectsDeploymentOwnedTrusterSettings(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"issuer_url":       func(c map[string]any) { c["issuer_url"] = "https://other.example.com" },
		"secrets.provider": func(c map[string]any) { c["secrets"].(map[string]any)["provider"] = "env" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testDraft(t, "aws")
			mutate(cfg.OIDC.Truster)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Validate() = %v, want error naming %s", err, name)
			}
		})
	}
	cfg := testDraft(t, "aws")
	cfg.OIDC.Truster = nil
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "oidc.truster is required") {
		t.Fatalf("Validate() = %v, want missing truster error", err)
	}
}

// TestSchemaJSONIsValidJSON checks the embedded schema's title and absence of generated-copy metadata.
func TestSchemaJSONIsValidJSON(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(SchemaJSON), &schema); err != nil {
		t.Fatalf("SchemaJSON is invalid JSON: %v", err)
	}
	if got := schema["title"]; got != "Podplane OIDC config" {
		t.Fatalf("schema title = %v, want Podplane OIDC config", got)
	}
	if _, ok := schema["$comment"]; ok {
		t.Fatal("source schema should not contain generated-copy $comment")
	}
}

// TestWriteAddsDefaultLocalSchemaRef checks default schema output without mutating the caller's config.
func TestWriteAddsDefaultLocalSchemaRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.oidc.jsonc")
	cfg := testDraft(t, "aws")
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("written config is invalid JSON: %v", err)
	}
	if got.Schema != DefaultSchemaRef {
		t.Fatalf("written schema ref = %q, want %q", got.Schema, DefaultSchemaRef)
	}
	if cfg.Schema != "" {
		t.Fatalf("Write mutated input schema ref to %q", cfg.Schema)
	}
}

// TestWriteSchemaWritesLocalSchemaFile checks the generated schema's source attribution.
func TestWriteSchemaWritesLocalSchemaFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSchema(filepath.Join(dir, "oidc.jsonc")); err != nil {
		t.Fatalf("WriteSchema returned error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, SchemaFileName))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("written schema is invalid JSON: %v", err)
	}
	comment, ok := schema["$comment"].(string)
	if !ok {
		t.Fatal("written schema missing generated-copy $comment")
	}
	if !strings.Contains(comment, "schemas/podplane.oidc.schema.json") {
		t.Fatalf("written schema $comment = %q, want source schema path", comment)
	}

	raw, err = os.ReadFile(filepath.Join(dir, trusterSchemaFileName))
	if err != nil {
		t.Fatal(err)
	}
	var truster any
	if err := json.Unmarshal(raw, &truster); err != nil {
		t.Fatalf("written Truster schema is invalid JSON: %v", err)
	}
	refs := 0
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				if target, ok := strings.CutPrefix(ref, filepath.Base(trusterSchemaFileName)+"#"); ok {
					refs++
					if resolvePointer(truster, target) == nil {
						t.Errorf("$ref %q does not resolve in the Truster schema", ref)
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(schema)
	if refs == 0 {
		t.Fatal("OIDC schema has no references into the Truster schema")
	}
}

// resolvePointer returns the value at the JSON Pointer pointer in doc, or nil
// when it does not exist.
func resolvePointer(doc any, pointer string) any {
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		object, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = object[strings.NewReplacer("~1", "/", "~0", "~").Replace(token)]
	}
	return doc
}

// TestNewDraftConfigRequiresProviderKind checks that a draft is never built
// for a cloud or login connector the caller did not choose.
func TestNewDraftConfigRequiresProviderKind(t *testing.T) {
	for _, kinds := range [][2]string{{"", "google"}, {"azure", "google"}, {"aws", ""}, {"aws", "email"}} {
		if _, err := NewDraftConfig(kinds[0], kinds[1]); err == nil {
			t.Fatalf("NewDraftConfig(%q, %q) returned no error", kinds[0], kinds[1])
		}
	}
}

// testDraft returns the draft config for providerKind or fails the test.
func testDraft(t *testing.T, providerKind string) *Config {
	t.Helper()
	cfg, err := NewDraftConfig(providerKind, "google")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
