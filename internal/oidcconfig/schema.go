// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidcconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/schemas"
)

// SchemaFileName is the local JSON Schema file written next to OIDC configs.
const SchemaFileName = "podplane.oidc.schema.json"

// DefaultSchemaRef is the relative schema reference embedded in new OIDC configs.
const DefaultSchemaRef = "./" + SchemaFileName

// SchemaJSON is the JSON Schema for podplane.oidc.jsonc files.
var SchemaJSON = schemas.OIDCSchemaJSON

// trusterSchemaFileName is the local copy of Truster's configuration schema,
// which the OIDC schema references for the oidc.truster settings.
const trusterSchemaFileName = "podplane.oidc.truster.schema.json"

// WriteSchema writes the local JSON Schema files used by editors for offline
// validation, completion, and hover documentation.
func WriteSchema(dir string) error {
	files := map[string]string{
		SchemaFileName:        schemas.GeneratedSchemaCopy(SchemaJSON, "schemas/podplane.oidc.schema.json"),
		trusterSchemaFileName: schemas.OIDCTrusterSchemaJSON,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
