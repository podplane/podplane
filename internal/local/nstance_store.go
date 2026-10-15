// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nstance-dev/nstance/v2/pkg/fakeserver"
)

// localNstanceStore persists fake Nstance keys beneath a local filesystem root.
type localNstanceStore struct {
	root string
}

// newLocalNstanceStore returns a filesystem-backed fake Nstance store scoped
// under Podplane's local data directory.
func newLocalNstanceStore(root string) (*localNstanceStore, error) {
	if root == "" {
		return nil, fmt.Errorf("store root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create store root: %w", err)
	}
	return &localNstanceStore{root: root}, nil
}

// path resolves a store key beneath the root, rejecting empty and escaping paths.
func (s *localNstanceStore) path(key string) (string, error) {
	clean := filepath.Clean(strings.TrimPrefix(key, "/"))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid fake nstance store key %q", key)
	}
	return filepath.Join(s.root, clean), nil
}

// Get returns the value for key, or fakeserver.ErrNotFound when it does not exist.
func (s *localNstanceStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fakeserver.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Put stores data for key, creating parent directories as needed.
func (s *localNstanceStore) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Delete removes key, or returns fakeserver.ErrNotFound when it does not exist.
func (s *localNstanceStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, fs.ErrNotExist) {
		return fakeserver.ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}
