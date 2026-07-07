// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package appbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectAppNameFromPackageJSON verifies scoped package names become image-safe.
func TestDetectAppNameFromPackageJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"@Example/My App"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectAppName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "example-my-app" {
		t.Fatalf("DetectAppName = %q, want example-my-app", got)
	}
}

// TestDetectAppNameFromGoModule verifies module paths use their final component.
func TestDetectAppNameFromGoModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/acme/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectAppName(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "api" {
		t.Fatalf("DetectAppName = %q, want api", got)
	}
}
