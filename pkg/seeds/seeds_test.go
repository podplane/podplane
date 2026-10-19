// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package seeds

import (
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestParseNameDefaultsAndValidates checks supported seed names, the default, and invalid input.
func TestParseNameDefaultsAndValidates(t *testing.T) {
	cases := map[string]string{
		"":       Agentic,
		Platform: Platform,
		Agentic:  Agentic,
		Minimal:  Minimal,
		None:     None,
	}
	for input, want := range cases {
		got, err := ParseName(input)
		if err != nil {
			t.Fatalf("ParseName(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseName(%q) = %q, want %q", input, got, want)
		}
	}
	for _, name := range []string{"bogus", "recommended", "app"} {
		if _, err := ParseName(name); err == nil {
			t.Fatalf("expected invalid seed name error for %q", name)
		}
	}
	for _, name := range []string{Platform, Agentic, Minimal, None, "", "recommended", "app", "bogus"} {
		if got, want := HasPlatformComponents(name), name == Platform || name == Agentic; got != want {
			t.Errorf("HasPlatformComponents(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestResolveSeedPathNoneSkips checks the none seed needs no download settings.
func TestResolveSeedPathNoneSkips(t *testing.T) {
	path, err := ResolveSeedPath(ResolveOptions{Name: None})
	if err != nil {
		t.Fatalf("ResolveSeedPath error = %v", err)
	}
	if path != "" {
		t.Fatalf("ResolveSeedPath path = %q, want empty", path)
	}
	configPath := filepath.Join(t.TempDir(), "cluster.jsonc")
	if err := os.WriteFile(configPath, []byte(`{"cluster":{"id":"testcluster","oidc":{"issuer_url":"https://oidc.example.com"},"seed":{"name":""}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	seed, err := ReadClusterSeed(configPath)
	if err != nil || seed.Name != None {
		t.Fatalf("ReadClusterSeed with stored empty name = %+v, %v; want none", seed, err)
	}
	if path, err := ResolveClusterSeedPath(ResolveClusterOptions{ClusterConfigPath: configPath}); err != nil || path != "" {
		t.Fatalf("ResolveClusterSeedPath with stored empty name = %q, %v; want no download", path, err)
	}
}

// TestVerifySeedFile checks matching SHA-512 verification and malformed digest rejection.
func TestVerifySeedFile(t *testing.T) {
	contents := []byte("seed snapshot")
	path := filepath.Join(t.TempDir(), "seed.netsy")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	sum := sha512.Sum512(contents)
	digest := "sha512:" + hex.EncodeToString(sum[:])
	if err := VerifySeedFile(path, digest); err != nil {
		t.Fatalf("VerifySeedFile returned error: %v", err)
	}
	if err := VerifySeedFile(path, "sha512:"+string(make([]byte, 128))); err == nil {
		t.Fatal("VerifySeedFile returned nil for invalid digest")
	}
}
