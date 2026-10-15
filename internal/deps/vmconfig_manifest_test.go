// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestManifestBootMetadata verifies JSON boot metadata decodes into complete kernel and initrd entries.
func TestManifestBootMetadata(t *testing.T) {
	const raw = `{
		"vmconfig": {
			"os": {
				"image": {"version": "20260501-2465", "url": "https://example.invalid/debian.qcow2", "type": "qcow2", "digest": "sha512:fa527f8a60f71e9f5d680cf7311b397d595fc95c36e059479b043a6accb9ef1c9eeacf7cd1b9feebe2145870f6da845e8a2a5171008c421f5c898a77d53725c3"},
				"boot": {
					"cmdline": "root=PARTUUID=27ea710e-9da4-4dcd-b856-ad8a3c60c395 ro",
					"kernel": {"partition": 1, "path": "/boot/vmlinuz-6.12.85+deb13-arm64", "digest": "sha256:e9f257ab255b40ec46f529a8660a4baa496fb4c4b49af206099427abbb3b9b4e"},
					"initrd": {"partition": 1, "path": "/boot/initrd.img-6.12.85+deb13-arm64", "digest": "sha256:5159830f33c41593a10f5fc72a49e10650724015ba251df34a217d054fb93ffa"}
				}
			}
		}
	}`

	var manifest Manifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	boot := manifest.VMConfig.OS.Boot
	if !boot.Complete() {
		t.Fatalf("boot metadata should be complete: %#v", boot)
	}
	if boot.Kernel.Partition != 1 || boot.Kernel.Path != "/boot/vmlinuz-6.12.85+deb13-arm64" {
		t.Fatalf("unexpected kernel metadata: %#v", boot.Kernel)
	}
	if boot.Initrd.Partition != 1 || boot.Initrd.Path != "/boot/initrd.img-6.12.85+deb13-arm64" {
		t.Fatalf("unexpected initrd metadata: %#v", boot.Initrd)
	}
}

// TestDependencyParseDigest verifies supported checksums split correctly and malformed digests report errors.
func TestDependencyParseDigest(t *testing.T) {
	const validSHA256Hex = "56171987d3947707c3563db2f4001bccaf50fd63468611b9f3cbecb1375ee7ec"
	const validSHA512Hex = "fa527f8a60f71e9f5d680cf7311b397d595fc95c36e059479b043a6accb9ef1c9eeacf7cd1b9feebe2145870f6da845e8a2a5171008c421f5c898a77d53725c3"

	tests := []struct {
		name        string
		digest      string
		wantAlgo    string
		wantHex     string
		wantErr     bool
		errContains string
	}{
		{
			name:     "valid sha256",
			digest:   "sha256:" + validSHA256Hex,
			wantAlgo: "sha256",
			wantHex:  validSHA256Hex,
		},
		{
			name:     "valid sha512",
			digest:   "sha512:" + validSHA512Hex,
			wantAlgo: "sha512",
			wantHex:  validSHA512Hex,
		},
		{
			name:        "empty digest",
			digest:      "",
			wantErr:     true,
			errContains: "missing digest",
		},
		{
			name:        "missing colon separator",
			digest:      "sha256" + validSHA256Hex,
			wantErr:     true,
			errContains: "invalid digest format",
		},
		{
			name:        "unsupported algorithm md5",
			digest:      "md5:d41d8cd98f00b204e9800998ecf8427e",
			wantErr:     true,
			errContains: "unsupported digest algorithm",
		},
		{
			name:        "sha256 with too-short hex",
			digest:      "sha256:abc123",
			wantErr:     true,
			errContains: "invalid sha256 hex length",
		},
		{
			name:        "sha256 with too-long hex",
			digest:      "sha256:" + validSHA256Hex + "00",
			wantErr:     true,
			errContains: "invalid sha256 hex length",
		},
		{
			name:        "sha256 with empty hex",
			digest:      "sha256:",
			wantErr:     true,
			errContains: "invalid sha256 hex length",
		},
		{
			name:        "sha512 with too-short hex",
			digest:      "sha512:abc123",
			wantErr:     true,
			errContains: "invalid sha512 hex length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Dependency{Digest: tt.digest}
			algo, hex, err := d.ParseDigest()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (returned %s:%s)", algo, hex)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if algo != tt.wantAlgo {
				t.Fatalf("algo: got %q, want %q", algo, tt.wantAlgo)
			}
			if hex != tt.wantHex {
				t.Fatalf("hex: got %q, want %q", hex, tt.wantHex)
			}
		})
	}
}

// TestDependencySHA256 verifies SHA-256 extraction rejects SHA-512 digests.
func TestDependencySHA256(t *testing.T) {
	const validHex = "56171987d3947707c3563db2f4001bccaf50fd63468611b9f3cbecb1375ee7ec"

	// SHA256() should work for sha256 digests
	d := Dependency{Digest: "sha256:" + validHex}
	got, err := d.SHA256()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != validHex {
		t.Fatalf("got %q, want %q", got, validHex)
	}

	// SHA256() should reject sha512 digests
	d = Dependency{Digest: "sha512:fa527f8a60f71e9f5d680cf7311b397d595fc95c36e059479b043a6accb9ef1c9eeacf7cd1b9feebe2145870f6da845e8a2a5171008c421f5c898a77d53725c3"}
	_, err = d.SHA256()
	if err == nil {
		t.Fatal("expected error for sha512 digest, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported digest algorithm") {
		t.Fatalf("error %q does not mention unsupported algorithm", err.Error())
	}
}

// TestItemResolveURL verifies upstream URLs remain unchanged unless a cache proxy base is supplied.
func TestItemResolveURL(t *testing.T) {
	it := Item{
		Name: "runc",
		Dep: Dependency{
			Version: "1.4.2",
			URL:     "https://github.com/opencontainers/runc/releases/download/v1.4.2/runc.arm64",
		},
	}

	// No base URL → use original upstream URL.
	if got := it.ResolveURL(""); got != it.Dep.URL {
		t.Errorf("ResolveURL(\"\") = %q, want %q", got, it.Dep.URL)
	}

	// With base URL → construct proxy URL.
	got := it.ResolveURL("http://10.0.2.2:1234/deps")
	want := "http://10.0.2.2:1234/deps/vmconfig/artifacts/runc/1.4.2/runc.arm64"
	if got != want {
		t.Errorf("ResolveURL(base) = %q, want %q", got, want)
	}
}

// TestManifestItemsFilterProvidersAndCachedState verifies install item selection respects providers and cache markers.
func TestManifestItemsFilterProvidersAndCachedState(t *testing.T) {
	manifest := &Manifest{VMConfig: VMConfig{Dependencies: map[string]Dependency{
		"neutral": {Version: "1", Cached: true},
		"aws":     {Version: "1", Providers: []string{"aws"}, Cached: true},
		"google":  {Version: "1", Providers: []string{"google"}, Cached: true},
		"missing": {Version: "1"},
	}}}

	items := manifest.InstallItems(ItemFilter{})
	if got, want := itemNames(items), "missing,neutral"; got != want {
		t.Fatalf("default filtered items = %s, want %s", got, want)
	}

	items = manifest.InstallItems(ItemFilter{Providers: []string{"aws"}})
	if got, want := itemNames(items), "aws,missing,neutral"; got != want {
		t.Fatalf("aws filtered items = %s, want %s", got, want)
	}

	items = manifest.InstallItems(ItemFilter{Providers: []string{"all"}, CachedOnly: true})
	if got, want := itemNames(items), "aws,google,neutral"; got != want {
		t.Fatalf("cached all filtered items = %s, want %s", got, want)
	}
}

// itemNames returns sorted item names joined for test comparisons.
func itemNames(items []Item) string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
