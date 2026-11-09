// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/schemas"
)

// SchemaFileName is the generated schema path relative to the OIDC config.
const SchemaFileName = ".podplane/oidc-schema.json"

// DefaultSchemaRef is the relative schema reference embedded in new OIDC configs.
const DefaultSchemaRef = "./" + SchemaFileName

// SchemaJSON is the JSON Schema for podplane.oidc.jsonc files.
var SchemaJSON = schemas.OIDCSchemaJSON

// trusterSchemaFileName is the local copy of Truster's configuration schema,
// which the OIDC schema references for the oidc.truster settings.
const trusterSchemaFileName = ".podplane/truster-schema.json"

// WriteSchema writes the local JSON Schema files used by editors for offline
// validation, completion, and hover documentation.
func WriteSchema(configPath string) error {
	dir := filepath.Dir(configPath)
	localSchema := schemas.GeneratedSchemaCopy(SchemaJSON, "schemas/podplane.oidc.schema.json")
	localSchema = strings.ReplaceAll(localSchema, "podplane.oidc.truster.schema.json#", "truster-schema.json#")
	// Resolve the bundled Truster schema locally rather than against the public schema ID.
	localSchema = strings.ReplaceAll(localSchema, "https://podplane.dev/schemas/podplane.oidc.schema.json", "oidc-schema.json")
	files := map[string]string{
		SchemaFileName:        localSchema,
		trusterSchemaFileName: schemas.OIDCTrusterSchemaJSON,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create schema directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
