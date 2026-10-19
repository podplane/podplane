// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestClusterSchemaJSONIsValidJSON checks the embedded schema title and absence of generated metadata.
func TestClusterSchemaJSONIsValidJSON(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(ClusterSchemaJSON), &schema); err != nil {
		t.Fatalf("ClusterSchemaJSON is invalid JSON: %v", err)
	}
	if got := schema["title"]; got != "Podplane cluster config" {
		t.Fatalf("schema title = %v, want Podplane cluster config", got)
	}
	if _, ok := schema["$comment"]; ok {
		t.Fatal("source schema should not contain generated-copy $comment")
	}
	seed := schema["$defs"].(map[string]any)["seed"].(map[string]any)
	names := seed["properties"].(map[string]any)["name"].(map[string]any)["enum"]
	if want := []any{"", "none", "minimal", "platform", "agentic"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("seed name enum = %v, want %v", names, want)
	}
	conditional := seed["if"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["enum"]
	if want := []any{"minimal", "platform", "agentic"}; !reflect.DeepEqual(conditional, want) {
		t.Fatalf("seed required metadata profiles = %v, want %v", conditional, want)
	}
}

// TestWriteAddsDefaultLocalSchemaRef checks that writing adds the offline schema reference without mutating input.
func TestWriteAddsDefaultLocalSchemaRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.cluster.jsonc")
	cfg, err := NewDraftConfig("aws")
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got ClusterConfig
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

// TestWriteSchemaWritesLocalSchemaFile checks that the schema copy identifies its checked-in source.
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
	if !strings.Contains(comment, "schemas/podplane.cluster.schema.json") {
		t.Fatalf("written schema $comment = %q, want source schema path", comment)
	}
}

// TestNetworkingSchemaCoversConfigFields checks schema properties match networking and pool JSON fields.
func TestNetworkingSchemaCoversConfigFields(t *testing.T) {
	var schema struct {
		Defs map[string]struct{ Properties map[string]json.RawMessage } `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(ClusterSchemaJSON), &schema); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{
		"cluster": Cluster{}, "provider": Provider{}, "tunnel": Tunnel{},
		"nat": NAT{}, "natGroup": NATGroup{}, "tenantNAT": TenantNAT{}, "listener": Listener{},
		"pool": Pool{},
	} {
		fields := reflect.TypeOf(value)
		properties := schema.Defs[name].Properties
		if len(properties) != fields.NumField() {
			t.Fatalf("schema %s has %d fields, config has %d", name, len(properties), fields.NumField())
		}
		for i := 0; i < fields.NumField(); i++ {
			key := strings.Split(fields.Field(i).Tag.Get("json"), ",")[0]
			if _, ok := properties[key]; !ok {
				t.Fatalf("schema %s missing config field %s", name, key)
			}
		}
	}
}
