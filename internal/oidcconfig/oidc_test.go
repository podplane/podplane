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

func TestLoadValidAWSConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podplane.oidc.jsonc")
	if err := os.WriteFile(path, []byte(`{
  // comments are allowed
  "oidc": {
    "provider": { "kind": "aws", "region": "us-east-1", "account": "123456789012", "profile": "default" },
    "hostname": "auth.example.com",
    "domain": { "zone": "example.com", "provider": { "kind": "aws" } },
    "connector": {
      "kind": "google",
      "client_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:connector"
    },
    "signing_key_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:signing",
    "encryption_key_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:encryption",
    "default_redirect_uris": ["http://localhost:8000"],
    "clients": {
      "kubelogin": { "groups_override": "prod" }
    },
    "groups_overrides": {
      "prod": { "ops@example.com": ["admins"] }
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
}

func TestValidateRejectsGoogleProvider(t *testing.T) {
	err := Validate(&Config{OIDC: OIDC{
		Provider: Provider{Kind: "google", Region: "us-central1"},
	}})
	if err == nil {
		t.Fatal("Validate returned nil, want error")
	}
}

func TestValidateRequiresEncryptionKeyForGitHub(t *testing.T) {
	cfg := NewDraftConfig("aws")
	cfg.OIDC.Connector.Kind = "github"
	cfg.OIDC.EncryptionKeySecretARN = ""
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "encryption_key_secret_arn") {
		t.Fatalf("Validate error = %v, want missing encryption key", err)
	}
}

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

func TestWriteAddsDefaultLocalSchemaRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.oidc.jsonc")
	cfg := NewDraftConfig("aws")
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

func TestWriteSchemaWritesLocalSchemaFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSchema(dir); err != nil {
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
}
