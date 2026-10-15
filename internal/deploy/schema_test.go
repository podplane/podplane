// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateTemplateValuesSchemaRequiresSchema checks that every template must provide a values schema.
func TestValidateTemplateValuesSchemaRequiresSchema(t *testing.T) {
	t.Parallel()
	err := validateTemplateValuesSchema("worker", t.TempDir(), false, false)
	if err == nil {
		t.Fatal("expected missing schema error")
	}
}

// TestValidateTemplateValuesSchemaRejectsUnsupportedRouteFlag checks rejection of hostname flags absent from the schema.
func TestValidateTemplateValuesSchemaRejectsUnsupportedRouteFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSchema(t, dir, `{"properties":{"image":{}}}`)
	err := validateTemplateValuesSchema("worker", dir, true, false)
	if err == nil {
		t.Fatal("expected unsupported flag error")
	}
}

// TestValidateTemplateValuesSchemaAcceptsSupportedRouteFlags checks acceptance of declared hostname and path properties.
func TestValidateTemplateValuesSchemaAcceptsSupportedRouteFlags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeSchema(t, dir, `{"properties":{"route":{"properties":{"hostname":{},"path":{}}}}}`)
	if err := validateTemplateValuesSchema("web", dir, true, true); err != nil {
		t.Fatal(err)
	}
}

// TestValidateTemplateValuesSchemaAcceptsChartArchive checks schema loading from a packaged chart.
func TestValidateTemplateValuesSchemaAcceptsChartArchive(t *testing.T) {
	t.Parallel()
	chartPath := filepath.Join(t.TempDir(), "web-1.0.0.tgz")
	writeChartArchive(t, chartPath, `{"properties":{"route":{"properties":{"hostname":{},"path":{}}}}}`)
	if err := validateTemplateValuesSchema("web", chartPath, true, true); err != nil {
		t.Fatal(err)
	}
}

// writeSchema writes a chart values schema or fails the test.
func writeSchema(t *testing.T, dir, schema string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, valuesSchemaFile), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeChartArchive creates a compressed chart containing the supplied values schema.
func writeChartArchive(t *testing.T, chartPath, schema string) {
	t.Helper()
	f, err := os.Create(chartPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	contents := []byte(schema)
	if err := tw.WriteHeader(&tar.Header{
		Name: "web/" + valuesSchemaFile,
		Mode: 0o600,
		Size: int64(len(contents)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
