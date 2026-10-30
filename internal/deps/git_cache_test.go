// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// TestEnsureComponentsGitCachedRepacksFetches checks that repeated fetches
// leave one packfile, because cross-pack delta cycles break serving the cache.
func TestEnsureComponentsGitCachedRepacksFetches(t *testing.T) {
	// Keep the developer's global Git config, such as commit signing, out.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	upstream := t.TempDir()
	repo, err := git.PlainInit(upstream, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commit := func(i int) plumbing.Hash {
		t.Helper()
		if err := os.WriteFile(filepath.Join(upstream, "values.yaml"), []byte("replicas: "+string(rune('0'+i))+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := worktree.Add("values.yaml"); err != nil {
			t.Fatal(err)
		}
		hash, err := worktree.Commit("commit", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(int64(i), 0)}})
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	commit(1)
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager("", t.TempDir())
	source := &ComponentsSource{URL: upstream, Ref: ComponentsSourceRef{Branch: head.Name().Short()}}
	ctx := context.Background()
	if err := m.EnsureComponentsGitCached(ctx, source); err != nil {
		t.Fatal(err)
	}
	var tip plumbing.Hash
	for i := 2; i <= 3; i++ {
		tip = commit(i)
		if err := m.EnsureComponentsGitCached(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	cachePath, err := m.ComponentsGitCachePath(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	packs, err := filepath.Glob(filepath.Join(cachePath, "objects", "pack", "*.pack"))
	if err != nil || len(packs) != 1 {
		t.Fatalf("cache has %d packfiles after fetches, want 1: %v", len(packs), err)
	}
	cached, err := git.PlainOpen(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := cached.Reference(head.Name(), true)
	if err != nil || ref.Hash() != tip {
		t.Fatalf("cached %s = %v, want latest commit %s: %v", head.Name(), ref, tip, err)
	}
	if _, err := cached.CommitObject(tip); err != nil {
		t.Fatalf("repack lost fetched objects: %v", err)
	}
}

// TestExactSemverTag verifies exact numeric release tags are accepted while ranges and prereleases are rejected.
func TestExactSemverTag(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantTag string
		wantOK  bool
	}{
		{name: "major minor", value: "1.2", wantTag: "1.2", wantOK: true},
		{name: "major minor patch", value: "1.2.3", wantTag: "1.2.3", wantOK: true},
		{name: "v prefix", value: "v1.2.3", wantTag: "v1.2.3", wantOK: true},
		{name: "trim whitespace", value: " v1.2.3 ", wantTag: "v1.2.3", wantOK: true},
		{name: "empty", value: "", wantOK: false},
		{name: "range", value: ">=1.0.0", wantOK: false},
		{name: "caret", value: "^1.2.3", wantOK: false},
		{name: "wildcard", value: "1.2.x", wantOK: false},
		{name: "prerelease", value: "1.2.3-beta.1", wantOK: false},
		{name: "major only", value: "1", wantOK: false},
		{name: "too many parts", value: "1.2.3.4", wantOK: false},
		{name: "empty part", value: "1..2", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTag, gotOK := exactSemverTag(tt.value)
			if gotOK != tt.wantOK {
				t.Fatalf("exactSemverTag(%q) ok = %v, want %v", tt.value, gotOK, tt.wantOK)
			}
			if gotTag != tt.wantTag {
				t.Fatalf("exactSemverTag(%q) tag = %q, want %q", tt.value, gotTag, tt.wantTag)
			}
		})
	}
}
