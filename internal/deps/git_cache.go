// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
)

// GitCacheDir returns the generic Git dependency cache directory.
func (m *Manager) GitCacheDir() string {
	return filepath.Join(m.depsCacheDir, "git")
}

// GitCachePath returns the cache path for a repository path under deps/git.
func (m *Manager) GitCachePath(repoPath string) string {
	return filepath.Join(m.GitCacheDir(), filepath.FromSlash(strings.TrimPrefix(repoPath, "/")))
}

// ComponentsGitCachePath returns the cache path for source URL.
func (m *Manager) ComponentsGitCachePath(sourceURL string) (string, error) {
	repoPath, err := GitCacheRepoPath(sourceURL)
	if err != nil {
		return "", err
	}
	return m.GitCachePath(repoPath), nil
}

// GitCacheRepoPath converts a Git remote URL to the canonical deps/git path.
func GitCacheRepoPath(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("components source url is required")
	}
	if strings.Contains(trimmed, "://") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("parse components source url: %w", err)
		}
		if u.Host == "" {
			return "", fmt.Errorf("components source url %q is missing host", rawURL)
		}
		return cleanGitRepoPath(path.Join(u.Host, u.Path)), nil
	}
	if before, after, ok := strings.Cut(trimmed, ":"); ok && strings.Contains(before, "@") && after != "" {
		host := strings.Split(before, "@")
		return cleanGitRepoPath(path.Join(host[len(host)-1], after)), nil
	}
	return cleanGitRepoPath(trimmed), nil
}

// EnsureComponentsGitCached clones or fetches the manifest-declared Git source.
func (m *Manager) EnsureComponentsGitCached(ctx context.Context, source *ComponentsSource) error {
	if source == nil {
		return nil
	}
	if err := validateComponentsSource(source); err != nil {
		return err
	}
	cachePath, err := m.ComponentsGitCachePath(source.URL)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cachePath, "config")); err == nil {
		repo, err := git.PlainOpen(cachePath)
		if err != nil {
			return fmt.Errorf("open cached components git repository: %w", err)
		}
		if err := repo.FetchContext(ctx, &git.FetchOptions{
			RemoteName: "origin",
			RemoteURL:  source.URL,
			RefSpecs:   []config.RefSpec{"+refs/*:refs/*"},
			Tags:       git.AllTags,
			Force:      true,
		}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			return fmt.Errorf("fetch cached components git repository: %w", err)
		}
		return verifyCachedComponentsGitRef(repo, source)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat cached components git repository: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return fmt.Errorf("create components git cache directory: %w", err)
	}
	repo, err := git.PlainCloneContext(ctx, cachePath, &git.CloneOptions{
		URL:    source.URL,
		Mirror: true,
		Bare:   true,
		Tags:   git.AllTags,
	})
	if err != nil {
		return fmt.Errorf("clone components git repository: %w", err)
	}
	return verifyCachedComponentsGitRef(repo, source)
}

// cleanGitRepoPath normalizes a repository cache path and prevents path escape.
func cleanGitRepoPath(value string) string {
	cleaned := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(value)), "/")
	return strings.TrimPrefix(cleaned, "/")
}

// verifyCachedComponentsGitRef verifies the requested source ref exists in cache.
func verifyCachedComponentsGitRef(repo *git.Repository, source *ComponentsSource) error {
	switch {
	case source.Ref.Branch != "":
		_, err := repo.Reference(plumbing.NewBranchReferenceName(source.Ref.Branch), true)
		if err != nil {
			return fmt.Errorf("cached components git repository is missing branch %q: %w", source.Ref.Branch, err)
		}
	case source.Ref.Tag != "":
		_, err := repo.Reference(plumbing.NewTagReferenceName(source.Ref.Tag), true)
		if err != nil {
			return fmt.Errorf("cached components git repository is missing tag %q: %w", source.Ref.Tag, err)
		}
	case source.Ref.Semver != "":
		if tag, ok := exactSemverTag(source.Ref.Semver); ok {
			if _, err := repo.Reference(plumbing.NewTagReferenceName(tag), true); err == nil {
				break
			}
			if !strings.HasPrefix(tag, "v") {
				if _, err := repo.Reference(plumbing.NewTagReferenceName("v"+tag), true); err == nil {
					break
				}
			}
			return fmt.Errorf("cached components git repository is missing semver tag %q", source.Ref.Semver)
		}
	case source.Ref.Commit != "":
		_, err := repo.CommitObject(plumbing.NewHash(source.Ref.Commit))
		if err != nil {
			return fmt.Errorf("cached components git repository is missing commit %q: %w", source.Ref.Commit, err)
		}
	}
	return nil
}

// exactSemverTag returns a tag to verify for exact semver selectors.
func exactSemverTag(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsAny(trimmed, "<>=~^* xX|") {
		return "", false
	}
	version := strings.TrimPrefix(trimmed, "v")
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return "", false
			}
		}
	}
	return trimmed, true
}
