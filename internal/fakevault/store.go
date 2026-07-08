// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	vaultKeyringPrefix = "dev.podplane.local."
	vaultKeyringSuffix = ".fakevault.key"
	vaultKeySize       = 32
	nonceSize          = 12
)

// Secret describes one fakevault secret without exposing secret values.
type Secret struct {
	Path     string
	Keys     []string
	Archived bool
	Version  int
}

// Store persists fakevault secrets.
type Store interface {
	SetSecret(clusterID, path string, values map[string]string) error
	GetSecret(clusterID, path string) (map[string]string, bool, error)
	ArchiveSecret(clusterID, path string) error
	RestoreSecret(clusterID, path string) error
	DeleteSecret(clusterID, path string) error
	ListSecrets(clusterID string) ([]Secret, error)
}

// KeyringBackend is the subset of Podplane config used by FileStore.
type KeyringBackend interface {
	KeyringWrite(key string, value []byte) error
	KeyringRead(key string) ([]byte, error)
}

// FileStore stores fakevault secrets as encrypted files protected by one
// keychain-backed vault key per local cluster.
type FileStore struct {
	backend KeyringBackend
	root    string

	mu   sync.Mutex
	keys map[string][]byte
}

type vaultKeyFile struct {
	Version   int    `json:"version"`
	Algorithm string `json:"algorithm"`
	Key       string `json:"key"`
}

type secretFile struct {
	Version    int      `json:"version"`
	Algorithm  string   `json:"algorithm"`
	Nonce      string   `json:"nonce"`
	Ciphertext string   `json:"ciphertext"`
	Keys       []string `json:"keys,omitempty"`
	Archived   bool     `json:"archived,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
}

// NewFileStore returns an encrypted file-backed fakevault store. root is the
// directory containing local cluster data, usually ~/.podplane/data/local.
func NewFileStore(backend KeyringBackend, root string) *FileStore {
	return &FileStore{backend: backend, root: root, keys: map[string][]byte{}}
}

// SetSecret writes a fakevault secret for clusterID and path.
func (s *FileStore) SetSecret(clusterID, path string, values map[string]string) error {
	clusterID, path, err := cleanClusterPath(clusterID, path)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return fmt.Errorf("at least one key=value pair is required")
	}
	data, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal fakevault secret: %w", err)
	}
	key, err := s.vaultKey(clusterID)
	if err != nil {
		return err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("create fakevault nonce: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create fakevault cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create fakevault AEAD: %w", err)
	}
	version := 1
	if current, ok, err := s.readFile(clusterID, path); err != nil {
		return err
	} else if ok && current.Version > 0 {
		version = current.Version + 1
	}
	file := secretFile{
		Version:    version,
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, data, []byte(path))),
		Keys:       sortedKeys(values),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	return s.writeFile(clusterID, path, file)
}

// GetSecret returns a fakevault secret for clusterID and path.
func (s *FileStore) GetSecret(clusterID, path string) (map[string]string, bool, error) {
	clusterID, path, err := cleanClusterPath(clusterID, path)
	if err != nil {
		return nil, false, err
	}
	file, ok, err := s.readFile(clusterID, path)
	if err != nil || !ok {
		return nil, false, err
	}
	if file.Archived {
		return nil, false, nil
	}
	values, err := s.decrypt(clusterID, path, file)
	if err != nil {
		return nil, false, err
	}
	return values, true, nil
}

// ArchiveSecret marks a fakevault secret archived without deleting its value.
func (s *FileStore) ArchiveSecret(clusterID, path string) error {
	return s.setArchived(clusterID, path, true)
}

// RestoreSecret makes an archived fakevault secret readable again.
func (s *FileStore) RestoreSecret(clusterID, path string) error {
	return s.setArchived(clusterID, path, false)
}

// DeleteSecret permanently removes a fakevault secret for clusterID and path.
func (s *FileStore) DeleteSecret(clusterID, path string) error {
	clusterID, path, err := cleanClusterPath(clusterID, path)
	if err != nil {
		return err
	}
	err = os.Remove(s.secretFilePath(clusterID, path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete fakevault secret file: %w", err)
	}
	return nil
}

// ListSecrets lists fakevault secrets for clusterID.
func (s *FileStore) ListSecrets(clusterID string) ([]Secret, error) {
	clusterID, err := cleanClusterID(clusterID)
	if err != nil {
		return nil, err
	}
	base := s.clusterDir(clusterID)
	var secrets []Secret
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat fakevault directory: %w", err)
	}
	if err := filepath.WalkDir(base, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(base, name)
		if err != nil {
			return err
		}
		path := filepath.ToSlash(strings.TrimSuffix(rel, ".json"))
		file, ok, err := s.readFile(clusterID, path)
		if err != nil || !ok {
			return err
		}
		keys := append([]string{}, file.Keys...)
		sort.Strings(keys)
		version := file.Version
		if version <= 0 {
			version = 1
		}
		secrets = append(secrets, Secret{Path: path, Keys: keys, Archived: file.Archived, Version: version})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk fakevault directory: %w", err)
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Path < secrets[j].Path })
	return secrets, nil
}

// CleanPath normalizes a Vault API path to the path stored by fakevault.
func CleanPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "v1/")
	return strings.Trim(path, "/")
}

// vaultKey returns the cluster's fakevault encryption key, loading it from the
// keychain once per local server process and generating it when absent.
func (s *FileStore) vaultKey(clusterID string) ([]byte, error) {
	clusterID, err := cleanClusterID(clusterID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key := s.keys[clusterID]; len(key) > 0 {
		return append([]byte(nil), key...), nil
	}
	itemKey := vaultKeyName(clusterID)
	data, err := s.backend.KeyringRead(itemKey)
	if err != nil {
		return nil, fmt.Errorf("read fakevault key: %w", err)
	}
	if len(data) == 0 {
		key := make([]byte, vaultKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("create fakevault key: %w", err)
		}
		payload, err := json.Marshal(vaultKeyFile{Version: 1, Algorithm: "aes-256-gcm", Key: base64.StdEncoding.EncodeToString(key)})
		if err != nil {
			return nil, fmt.Errorf("marshal fakevault key: %w", err)
		}
		if err := s.backend.KeyringWrite(itemKey, payload); err != nil {
			return nil, fmt.Errorf("write fakevault key: %w", err)
		}
		s.keys[clusterID] = append([]byte(nil), key...)
		return key, nil
	}
	var payload vaultKeyFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode fakevault key: %w", err)
	}
	if payload.Algorithm != "aes-256-gcm" {
		return nil, fmt.Errorf("unsupported fakevault key algorithm %q", payload.Algorithm)
	}
	key, err := base64.StdEncoding.DecodeString(payload.Key)
	if err != nil {
		return nil, fmt.Errorf("decode fakevault key bytes: %w", err)
	}
	if len(key) != vaultKeySize {
		return nil, fmt.Errorf("fakevault key has %d bytes, want %d", len(key), vaultKeySize)
	}
	s.keys[clusterID] = append([]byte(nil), key...)
	return key, nil
}

// decrypt decrypts one stored fakevault secret file.
func (s *FileStore) decrypt(clusterID, path string, file secretFile) (map[string]string, error) {
	if file.Algorithm != "aes-256-gcm" {
		return nil, fmt.Errorf("unsupported fakevault secret algorithm %q", file.Algorithm)
	}
	key, err := s.vaultKey(clusterID)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(file.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode fakevault nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(file.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode fakevault ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create fakevault cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create fakevault AEAD: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(path))
	if err != nil {
		return nil, fmt.Errorf("decrypt fakevault secret: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal(plaintext, &values); err != nil {
		return nil, fmt.Errorf("decode fakevault secret %q: %w", path, err)
	}
	return values, nil
}

// setArchived updates the archived flag for a fakevault secret file.
func (s *FileStore) setArchived(clusterID, path string, archived bool) error {
	clusterID, path, err := cleanClusterPath(clusterID, path)
	if err != nil {
		return err
	}
	file, ok, err := s.readFile(clusterID, path)
	if err != nil || !ok {
		return err
	}
	file.Archived = archived
	file.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return s.writeFile(clusterID, path, file)
}

// readFile reads one fakevault secret file.
func (s *FileStore) readFile(clusterID, path string) (secretFile, bool, error) {
	data, err := os.ReadFile(s.secretFilePath(clusterID, path))
	if os.IsNotExist(err) {
		return secretFile{}, false, nil
	}
	if err != nil {
		return secretFile{}, false, fmt.Errorf("read fakevault secret file: %w", err)
	}
	var file secretFile
	if err := json.Unmarshal(data, &file); err != nil {
		return secretFile{}, false, fmt.Errorf("decode fakevault secret file: %w", err)
	}
	return file, true, nil
}

// writeFile writes one fakevault secret file.
func (s *FileStore) writeFile(clusterID, path string, file secretFile) error {
	name := s.secretFilePath(clusterID, path)
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return fmt.Errorf("create fakevault directory: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fakevault secret file: %w", err)
	}
	if err := os.WriteFile(name, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write fakevault secret file: %w", err)
	}
	return nil
}

// secretFilePath returns the encrypted file path for a clean fakevault path.
func (s *FileStore) secretFilePath(clusterID, path string) string {
	return filepath.Join(append([]string{s.clusterDir(clusterID)}, strings.Split(path, "/")...)...) + ".json"
}

// clusterDir returns the fakevault directory for a local cluster.
func (s *FileStore) clusterDir(clusterID string) string {
	return filepath.Join(s.root, clusterID, "fakevault")
}

// cleanClusterPath validates and normalizes a cluster ID and fakevault path.
func cleanClusterPath(clusterID, path string) (string, string, error) {
	cleanID, err := cleanClusterID(clusterID)
	if err != nil {
		return "", "", err
	}
	path = CleanPath(path)
	if path == "" {
		return "", "", fmt.Errorf("secret path is required")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\`) {
			return "", "", fmt.Errorf("invalid secret path %q", path)
		}
	}
	return cleanID, path, nil
}

// cleanClusterID validates and normalizes a local fakevault cluster ID.
func cleanClusterID(clusterID string) (string, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return "", fmt.Errorf("cluster ID is required")
	}
	if clusterID == "." || clusterID == ".." || strings.ContainsAny(clusterID, `/\`) {
		return "", fmt.Errorf("invalid cluster ID %q", clusterID)
	}
	return clusterID, nil
}

// vaultKeyName returns the keychain item name for a cluster fakevault key.
func vaultKeyName(clusterID string) string {
	return vaultKeyringPrefix + clusterID + vaultKeyringSuffix
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
