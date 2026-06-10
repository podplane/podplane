// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package infrafiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryEmpty(t *testing.T) {
	dir := t.TempDir()
	empty, err := directoryEmpty(dir)
	if err != nil {
		t.Fatalf("directoryEmpty returned error: %v", err)
	}
	if !empty {
		t.Fatal("new temp directory was not empty")
	}
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty, err = directoryEmpty(dir)
	if err != nil {
		t.Fatalf("directoryEmpty returned error: %v", err)
	}
	if empty {
		t.Fatal("directory with a file was reported empty")
	}
	empty, err = directoryEmpty(filepath.Join(dir, "missing"))
	if err != nil {
		t.Fatalf("directoryEmpty returned error for missing directory: %v", err)
	}
	if !empty {
		t.Fatal("missing directory was not treated as empty")
	}
}

func TestValidateOutputDirectory(t *testing.T) {
	dir := t.TempDir()
	validate := validateOutputDirectory(dir)
	for _, value := range []string{"", "   "} {
		if err := validate(value); err == nil {
			t.Fatalf("validateOutputDirectory(%q) returned nil error", value)
		}
	}
	if err := validate("cluster"); err != nil {
		t.Fatalf("validateOutputDirectory returned error for new directory: %v", err)
	}
	if err := validate("cluster/nested"); err != nil {
		t.Fatalf("validateOutputDirectory returned error for nested new directory: %v", err)
	}
	existing := filepath.Join(dir, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validate("existing"); err != nil {
		t.Fatalf("validateOutputDirectory returned error for empty existing directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validate("existing"); err != nil {
		t.Fatalf("validateOutputDirectory returned error for non-empty directory: %v", err)
	}
	filePath := filepath.Join(dir, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validate("file"); err == nil {
		t.Fatal("validateOutputDirectory returned nil for file path")
	}
}

func TestResolveOutputDirectory(t *testing.T) {
	dir := t.TempDir()
	if got, want := resolveOutputDirectory(dir, "my-cluster"), filepath.Join(dir, "my-cluster"); got != want {
		t.Fatalf("resolveOutputDirectory relative path = %q, want %q", got, want)
	}
	abs := filepath.Join(dir, "absolute")
	if got := resolveOutputDirectory("/different", abs); got != abs {
		t.Fatalf("resolveOutputDirectory absolute path = %q, want %q", got, abs)
	}
}
