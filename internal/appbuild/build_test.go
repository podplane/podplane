// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package appbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureBuildFileGeneratesSelfContainedTemplate verifies automatic Containerfile generation.
func TestEnsureBuildFileGeneratesSelfContainedTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/acme/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, reason, err := EnsureBuildFile(dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "go.mod found" {
		t.Fatalf("reason = %q, want go.mod found", reason)
	}
	if len(written) != 1 || written[0] != "Containerfile" {
		t.Fatalf("written = %v, want Containerfile", written)
	}
	if _, err := os.Stat(filepath.Join(dir, "Containerfile")); err != nil {
		t.Fatalf("Containerfile was not written: %v", err)
	}
}

// TestNoBuildFileErrorEducatesAboutDockerfile verifies the missing-file error mentions both names.
func TestNoBuildFileErrorEducatesAboutDockerfile(t *testing.T) {
	for _, want := range []string{"Containerfile or Dockerfile", "OCI-neutral", "still use Dockerfile"} {
		if !strings.Contains(NoBuildFileError, want) {
			t.Fatalf("error = %q, want %q", NoBuildFileError, want)
		}
	}
}
