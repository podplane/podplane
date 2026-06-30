// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package dockerconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSetCredentialHelperCreatesDockerConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setDockerInstalled(t, true)

	configured, err := SetCredentialHelper("registry.example.com")
	if err != nil {
		t.Fatalf("SetCredentialHelper error = %v", err)
	}
	if !configured {
		t.Fatal("SetCredentialHelper configured = false, want true")
	}
	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".docker", "config.json"))
	if err != nil {
		t.Fatalf("read Docker config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode Docker config: %v", err)
	}
	credHelpers := cfg["credHelpers"].(map[string]any)
	if got, want := credHelpers["registry.example.com"], "podplane"; got != want {
		t.Fatalf("credHelpers entry = %v, want %v", got, want)
	}
}

func TestSetCredentialHelperUpdatesExistingDockerConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setDockerInstalled(t, true)
	dockerDir := filepath.Join(home, ".docker")
	if err := os.MkdirAll(dockerDir, 0o700); err != nil {
		t.Fatalf("create Docker config dir: %v", err)
	}
	configPath := filepath.Join(dockerDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "auths": {"example.com": {}},
  "credHelpers": {"other.example.com": "osxkeychain"}
}`), 0o600); err != nil {
		t.Fatalf("write Docker config: %v", err)
	}

	configured, err := SetCredentialHelper("registry.example.com")
	if err != nil {
		t.Fatalf("SetCredentialHelper error = %v", err)
	}
	if !configured {
		t.Fatal("SetCredentialHelper configured = false, want true")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Docker config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode Docker config: %v", err)
	}
	if _, ok := cfg["auths"].(map[string]any)["example.com"]; !ok {
		t.Fatalf("auths entry was not preserved: %#v", cfg["auths"])
	}
	credHelpers := cfg["credHelpers"].(map[string]any)
	if got, want := credHelpers["other.example.com"], "osxkeychain"; got != want {
		t.Fatalf("existing credHelpers entry = %v, want %v", got, want)
	}
	if got, want := credHelpers["registry.example.com"], "podplane"; got != want {
		t.Fatalf("new credHelpers entry = %v, want %v", got, want)
	}
}

func TestSetCredentialHelperPreservesExistingHelperForHostname(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setDockerInstalled(t, true)
	dockerDir := filepath.Join(home, ".docker")
	if err := os.MkdirAll(dockerDir, 0o700); err != nil {
		t.Fatalf("create Docker config dir: %v", err)
	}
	configPath := filepath.Join(dockerDir, "config.json")
	original := []byte(`{
  "credHelpers": {"registry.example.com": "osxkeychain"}
}`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write Docker config: %v", err)
	}

	configured, err := SetCredentialHelper("registry.example.com")
	if err != nil {
		t.Fatalf("SetCredentialHelper error = %v", err)
	}
	if configured {
		t.Fatal("SetCredentialHelper configured = true, want false")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read Docker config: %v", err)
	}
	if string(raw) != string(original) {
		t.Fatalf("Docker config was changed:\n%s", raw)
	}
}

func TestSetCredentialHelperSkipsWhenDockerMissing(t *testing.T) {
	setDockerInstalled(t, false)

	configured, err := SetCredentialHelper("registry.example.com")
	if err != nil {
		t.Fatalf("SetCredentialHelper error = %v", err)
	}
	if configured {
		t.Fatal("SetCredentialHelper configured = true, want false")
	}
}

// setDockerInstalled stubs Docker CLI availability for a test.
func setDockerInstalled(t *testing.T, installed bool) {
	t.Helper()
	originalLookPath := lookPath
	lookPath = func(string) (string, error) {
		if installed {
			return "/usr/local/bin/docker", nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { lookPath = originalLookPath })
}
