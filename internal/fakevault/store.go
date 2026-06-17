// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const keyringPrefix = "dev.podplane.fakevault."

// Secret describes one keyring-backed fakevault secret.
type Secret struct {
	Path string
	Keys []string
}

// Store persists fakevault secrets.
type Store interface {
	SetSecret(clusterID, path string, values map[string]string) error
	GetSecret(clusterID, path string) (map[string]string, bool, error)
	DeleteSecret(clusterID, path string) error
	ListSecrets(clusterID string) ([]Secret, error)
}

// KeyringBackend is the subset of Podplane config used by KeyringStore.
type KeyringBackend interface {
	KeyringWrite(key string, value []byte) error
	KeyringRead(key string) ([]byte, error)
	KeyringDelete(key string) error
}

// KeyringStore stores fakevault secrets in a keyring backend.
type KeyringStore struct {
	backend KeyringBackend
}

type indexEntry struct {
	Path string   `json:"path"`
	Keys []string `json:"keys"`
}

// NewKeyringStore returns a keyring-backed fakevault store.
func NewKeyringStore(backend KeyringBackend) *KeyringStore {
	return &KeyringStore{backend: backend}
}

// SetSecret writes a fakevault secret for clusterID and path.
func (s *KeyringStore) SetSecret(clusterID, path string, values map[string]string) error {
	clusterID = strings.TrimSpace(clusterID)
	path = CleanPath(path)
	if clusterID == "" {
		return fmt.Errorf("cluster ID is required")
	}
	if path == "" {
		return fmt.Errorf("secret path is required")
	}
	if len(values) == 0 {
		return fmt.Errorf("at least one key=value pair is required")
	}

	data, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal fakevault secret: %w", err)
	}
	if err := s.backend.KeyringWrite(secretKey(clusterID, path), data); err != nil {
		return fmt.Errorf("write fakevault keyring entry: %w", err)
	}

	index, err := s.readIndex(clusterID)
	if err != nil {
		return err
	}
	index[encodePath(path)] = indexEntry{Path: path, Keys: sortedKeys(values)}
	return s.writeIndex(clusterID, index)
}

// GetSecret returns a fakevault secret for clusterID and path.
func (s *KeyringStore) GetSecret(clusterID, path string) (map[string]string, bool, error) {
	clusterID = strings.TrimSpace(clusterID)
	path = CleanPath(path)
	if clusterID == "" {
		return nil, false, fmt.Errorf("cluster ID is required")
	}
	if path == "" {
		return nil, false, fmt.Errorf("secret path is required")
	}

	data, err := s.backend.KeyringRead(secretKey(clusterID, path))
	if err != nil {
		return nil, false, fmt.Errorf("read fakevault keyring entry: %w", err)
	}
	if len(data) == 0 {
		return nil, false, nil
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, false, fmt.Errorf("decode fakevault secret %q: %w", path, err)
	}
	return values, true, nil
}

// DeleteSecret removes a fakevault secret for clusterID and path.
func (s *KeyringStore) DeleteSecret(clusterID, path string) error {
	clusterID = strings.TrimSpace(clusterID)
	path = CleanPath(path)
	if clusterID == "" {
		return fmt.Errorf("cluster ID is required")
	}
	if path == "" {
		return fmt.Errorf("secret path is required")
	}

	if err := s.backend.KeyringDelete(secretKey(clusterID, path)); err != nil {
		return fmt.Errorf("delete fakevault keyring entry: %w", err)
	}
	index, err := s.readIndex(clusterID)
	if err != nil {
		return err
	}
	delete(index, encodePath(path))
	return s.writeIndex(clusterID, index)
}

// ListSecrets lists fakevault secrets for clusterID.
func (s *KeyringStore) ListSecrets(clusterID string) ([]Secret, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return nil, fmt.Errorf("cluster ID is required")
	}
	index, err := s.readIndex(clusterID)
	if err != nil {
		return nil, err
	}
	secrets := make([]Secret, 0, len(index))
	for _, entry := range index {
		keys := append([]string{}, entry.Keys...)
		sort.Strings(keys)
		secrets = append(secrets, Secret{Path: entry.Path, Keys: keys})
	}
	sort.Slice(secrets, func(i, j int) bool {
		return secrets[i].Path < secrets[j].Path
	})
	return secrets, nil
}

// readIndex returns the stored secret index for a cluster.
func (s *KeyringStore) readIndex(clusterID string) (map[string]indexEntry, error) {
	data, err := s.backend.KeyringRead(indexKey(clusterID))
	if err != nil {
		return nil, fmt.Errorf("read fakevault index: %w", err)
	}
	if len(data) == 0 {
		return map[string]indexEntry{}, nil
	}
	var index map[string]indexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("decode fakevault index: %w", err)
	}
	if index == nil {
		index = map[string]indexEntry{}
	}
	return index, nil
}

// writeIndex writes the stored secret index for a cluster.
func (s *KeyringStore) writeIndex(clusterID string, index map[string]indexEntry) error {
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("marshal fakevault index: %w", err)
	}
	if err := s.backend.KeyringWrite(indexKey(clusterID), data); err != nil {
		return fmt.Errorf("write fakevault index: %w", err)
	}
	return nil
}

// CleanPath normalizes a Vault API path to the key stored by fakevault.
func CleanPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "v1/")
	return strings.Trim(path, "/")
}

// secretKey returns the keyring key for a secret path.
func secretKey(clusterID, path string) string {
	return keyringPrefix + clusterID + ":" + path
}

// indexKey returns the keyring key for a cluster's secret index.
func indexKey(clusterID string) string {
	return keyringPrefix + clusterID + ":_index"
}

// encodePath returns a key-safe encoding for a secret path.
func encodePath(path string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(path))
}

// sortedKeys returns the sorted keys in a secret value map.
func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
