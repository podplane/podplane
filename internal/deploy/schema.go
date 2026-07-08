// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const valuesSchemaFile = "values.schema.json"

type valuesSchema struct {
	Properties map[string]valuesSchema `json:"properties"`
}

// validateTemplateValuesSchema verifies that the chart has a values schema and
// that any requested ergonomic route flags are supported by that schema.
func validateTemplateValuesSchema(template, chartPath string, needsHostname, needsPath bool) error {
	schema, err := readValuesSchema(chartPath)
	if err != nil {
		return err
	}
	if needsHostname && !schema.hasPath("route.hostname") {
		return unsupportedTemplateFlagError(template, "--hostname")
	}
	if needsPath && !schema.hasPath("route.path") {
		return unsupportedTemplateFlagError(template, "--path")
	}
	return nil
}

// readValuesSchema loads the required Helm values schema from chartPath.
func readValuesSchema(chartPath string) (valuesSchema, error) {
	raw, err := readValuesSchemaFile(chartPath)
	if err != nil {
		return valuesSchema{}, err
	}
	var schema valuesSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return valuesSchema{}, fmt.Errorf("parse template values schema: %w", err)
	}
	return schema, nil
}

// readValuesSchemaFile reads the raw values schema from either an unpacked
// chart directory or a packaged Helm chart archive.
func readValuesSchemaFile(chartPath string) ([]byte, error) {
	info, err := os.Stat(chartPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("template chart %q is missing required %s", chartPath, valuesSchemaFile)
		}
		return nil, fmt.Errorf("read template values schema: %w", err)
	}
	if !info.IsDir() {
		return readValuesSchemaArchive(chartPath)
	}
	raw, err := os.ReadFile(filepath.Join(chartPath, valuesSchemaFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("template chart %q is missing required %s", chartPath, valuesSchemaFile)
		}
		return nil, fmt.Errorf("read template values schema: %w", err)
	}
	return raw, nil
}

// readValuesSchemaArchive reads the raw values schema from a packaged Helm
// chart archive.
func readValuesSchemaArchive(chartPath string) ([]byte, error) {
	f, err := os.Open(chartPath)
	if err != nil {
		return nil, fmt.Errorf("read template values schema: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read template values schema archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read template values schema archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg || path.Base(header.Name) != valuesSchemaFile {
			continue
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read template values schema archive: %w", err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("template chart %q is missing required %s", chartPath, valuesSchemaFile)
}

// hasPath reports whether the schema declares a dot-separated property path.
func (s valuesSchema) hasPath(path string) bool {
	current := s
	for _, part := range strings.Split(path, ".") {
		next, ok := current.Properties[part]
		if !ok {
			return false
		}
		current = next
	}
	return true
}

// unsupportedTemplateFlagError formats a user-facing error for unsupported
// template-dependent deploy flags.
func unsupportedTemplateFlagError(template, flag string) error {
	if template == "" {
		template = "template"
	}
	return fmt.Errorf("template %q does not support %s", template, flag)
}
