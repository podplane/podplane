// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUserFromContextUsesSelectedContext checks that an explicit context overrides the current context.
func TestUserFromContextUsesSelectedContext(t *testing.T) {
	installFakeKubectl(t, `{"current-context":"dev","contexts":[{"name":"dev","context":{"user":"dev-user"}},{"name":"prod","context":{"user":"prod-user"}}]}`)
	user, err := UserFromContext("prod", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := user, "prod-user"; got != want {
		t.Fatalf("UserFromContext() = %q, want %q", got, want)
	}
}

// TestUserFromContextDefaultsToCurrentContext checks user lookup without an explicit context.
func TestUserFromContextDefaultsToCurrentContext(t *testing.T) {
	installFakeKubectl(t, `{"current-context":"dev","contexts":[{"name":"dev","context":{"user":"dev-user"}}]}`)
	user, err := UserFromContext("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := user, "dev-user"; got != want {
		t.Fatalf("UserFromContext() = %q, want %q", got, want)
	}
}

// installFakeKubectl prepends a shell executable that emits canned output to the test PATH.
func installFakeKubectl(t *testing.T, stdout string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\nprintf '%s' '" + strings.ReplaceAll(stdout, "'", "'\\''") + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
