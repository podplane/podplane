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

func TestParseNameDefaultsAndValidates(t *testing.T) {
	cases := map[string]string{
		"":          Recommended,
		Recommended: Recommended,
		Minimal:     Minimal,
		None:        None,
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
	if _, err := ParseName("bogus"); err == nil {
		t.Fatalf("expected invalid seed name error")
	}
}

func TestResolveSeedPathNoneSkips(t *testing.T) {
	path, err := ResolveSeedPath(ResolveOptions{Name: None})
	if err != nil {
		t.Fatalf("ResolveSeedPath error = %v", err)
	}
	if path != "" {
		t.Fatalf("ResolveSeedPath path = %q, want empty", path)
	}
}

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
