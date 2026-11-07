// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Package schemas embeds Podplane JSON Schemas for offline CLI output.
package schemas

import (
	"fmt"
	"strings"

	_ "embed"
)

// ClusterSchemaJSON is the JSON Schema for podplane.cluster.jsonc files.
//
//go:embed podplane.cluster.schema.json
var ClusterSchemaJSON string

// OIDCSchemaJSON is the JSON Schema for podplane.oidc.jsonc files.
//
//go:embed podplane.oidc.schema.json
var OIDCSchemaJSON string

// OIDCTrusterSchemaJSON is a verbatim copy of the Truster configuration
// schema for the pinned Truster release, referenced by OIDCSchemaJSON.
//
//go:embed podplane.oidc.truster.schema.json
var OIDCTrusterSchemaJSON string

// GeneratedSchemaCopy returns schema with a top-level JSON Schema $comment for
// local generated copies written beside user-authored config files.
func GeneratedSchemaCopy(schema string, sourcePath string) string {
	comment := fmt.Sprintf("  \"$comment\": \"Podplane generated schema support file. Do not edit generated copies directly; update %s in the Podplane repository instead.\",\n", sourcePath)
	if index := strings.Index(schema, "\n  \"$id\": "); index >= 0 {
		nextLine := strings.Index(schema[index+1:], "\n")
		if nextLine >= 0 {
			insert := index + 1 + nextLine + 1
			return schema[:insert] + comment + schema[insert:]
		}
	}
	if strings.HasPrefix(schema, "{\n") {
		return "{\n" + comment + schema[2:]
	}
	return schema
}
