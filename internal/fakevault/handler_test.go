// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

type memoryStore struct {
	secrets  map[string]map[string]string
	archived map[string]bool
	versions map[string]int
}

// rejectToken always rejects login JWTs for handler tests.
func rejectToken(context.Context, string, string, string) error {
	return fmt.Errorf("rejected")
}

// SetSecret stores a fakevault secret in memory for handler tests.
func (s *memoryStore) SetSecret(clusterID, path string, values map[string]string) error {
	if s.secrets == nil {
		s.secrets = map[string]map[string]string{}
	}
	if s.versions == nil {
		s.versions = map[string]int{}
	}
	key := clusterID + ":" + CleanPath(path)
	s.secrets[key] = values
	s.versions[key]++
	delete(s.archived, key)
	return nil
}

// CreateSecret stores a fakevault secret only when it does not already exist.
func (s *memoryStore) CreateSecret(clusterID, path string, values map[string]string) (bool, error) {
	if s.secrets == nil {
		s.secrets = map[string]map[string]string{}
	}
	key := clusterID + ":" + CleanPath(path)
	if _, ok := s.secrets[key]; ok {
		return false, nil
	}
	s.secrets[key] = values
	if s.versions == nil {
		s.versions = map[string]int{}
	}
	s.versions[key] = 1
	return true, nil
}

// CompareAndSetSecret stores a fakevault secret when version matches.
func (s *memoryStore) CompareAndSetSecret(clusterID, path string, version int, values map[string]string) (bool, error) {
	key := clusterID + ":" + CleanPath(path)
	if _, ok := s.secrets[key]; !ok {
		return false, nil
	}
	currentVersion := s.versions[key]
	if currentVersion == 0 {
		currentVersion = 1
	}
	if currentVersion != version {
		return false, nil
	}
	if s.versions == nil {
		s.versions = map[string]int{}
	}
	s.secrets[key] = values
	s.versions[key] = currentVersion + 1
	delete(s.archived, key)
	return true, nil
}

// GetSecret returns a fakevault secret from memory for handler tests.
func (s *memoryStore) GetSecret(clusterID, path string) (map[string]string, bool, error) {
	key := clusterID + ":" + CleanPath(path)
	if s.archived[key] {
		return nil, false, nil
	}
	values, ok := s.secrets[key]
	return values, ok, nil
}

// ArchiveSecret archives a fakevault secret in memory for handler tests.
func (s *memoryStore) ArchiveSecret(clusterID, path string) error {
	if s.archived == nil {
		s.archived = map[string]bool{}
	}
	s.archived[clusterID+":"+CleanPath(path)] = true
	return nil
}

// RestoreSecret restores an archived fakevault secret in memory for handler tests.
func (s *memoryStore) RestoreSecret(clusterID, path string) error {
	delete(s.archived, clusterID+":"+CleanPath(path))
	return nil
}

// DeleteSecret removes a fakevault secret from memory for handler tests.
func (s *memoryStore) DeleteSecret(clusterID, path string) error {
	key := clusterID + ":" + CleanPath(path)
	delete(s.secrets, key)
	delete(s.archived, key)
	delete(s.versions, key)
	return nil
}

// DeleteCluster removes all fakevault secrets belonging to clusterID.
func (s *memoryStore) DeleteCluster(clusterID string) error {
	for key := range s.secrets {
		entryClusterID, _, _ := strings.Cut(key, ":")
		if entryClusterID == clusterID {
			delete(s.secrets, key)
			delete(s.archived, key)
			delete(s.versions, key)
		}
	}
	return nil
}

// ListSecrets lists fakevault secrets from memory for handler tests.
func (s *memoryStore) ListSecrets(clusterID string) ([]Secret, error) {
	var secrets []Secret
	for key, values := range s.secrets {
		entryClusterID, path, ok := strings.Cut(key, ":")
		if !ok || entryClusterID != clusterID {
			continue
		}
		version := s.versions[key]
		if version == 0 {
			version = 1
		}
		secrets = append(secrets, Secret{Path: path, Keys: sortedKeys(values), Archived: s.archived[key], Version: version})
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Path < secrets[j].Path })
	return secrets, nil
}

// TestHandlerLoginAndReadSecret verifies login and authenticated KV reads.
func TestHandlerLoginAndReadSecret(t *testing.T) {
	store := &memoryStore{secrets: map[string]map[string]string{
		"dev:secret/data/apps/app/db": {"password": "secret"},
	}}
	handler := NewHandler(store, nil)

	loginBody := bytes.NewBufferString(`{"jwt":"token","role":"app"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/vault/dev/v1/auth/kubernetes/login", loginBody)
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Auth.ClientToken == "" {
		t.Fatalf("login response missing client token")
	}

	readReq := httptest.NewRequest(http.MethodGet, "/vault/dev/v1/secret/data/apps/app/db", nil)
	readReq.Header.Set("X-Vault-Token", loginResp.Auth.ClientToken)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readRec.Code, readRec.Body.String())
	}
	var readResp struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &readResp); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if readResp.Data.Data["password"] != "secret" {
		t.Fatalf("password = %q, want secret", readResp.Data.Data["password"])
	}
}

// TestHandlerRejectsCrossClusterToken verifies issued tokens stay cluster-scoped.
func TestHandlerRejectsCrossClusterToken(t *testing.T) {
	store := &memoryStore{secrets: map[string]map[string]string{
		"other:secret/data/apps/app/db": {"password": "secret"},
	}}
	handler := NewHandler(store, nil)

	loginReq := httptest.NewRequest(http.MethodPost, "/vault/dev/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"app"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	var loginResp struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	readReq := httptest.NewRequest(http.MethodGet, "/vault/other/v1/secret/data/apps/app/db", nil)
	readReq.Header.Set("X-Vault-Token", loginResp.Auth.ClientToken)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusForbidden {
		t.Fatalf("read status = %d, want %d", readRec.Code, http.StatusForbidden)
	}
}

// TestHandlerRejectsUnscopedVaultPath verifies /vault/v1 requests are rejected.
func TestHandlerRejectsUnscopedVaultPath(t *testing.T) {
	store := &memoryStore{secrets: map[string]map[string]string{
		"dev:secret/data/apps/app/db": {"password": "secret"},
	}}
	handler := NewHandler(store, nil)

	loginReq := httptest.NewRequest(http.MethodPost, "/vault/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"app"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusNotFound {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusNotFound)
	}
}

// TestHandlerRejectsInvalidLoginJWT verifies validator failures reject login.
func TestHandlerRejectsInvalidLoginJWT(t *testing.T) {
	handler := NewHandler(&memoryStore{}, rejectToken)
	loginReq := httptest.NewRequest(http.MethodPost, "/vault/dev/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"app"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusForbidden {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusForbidden)
	}
}

// TestHandlerWriteListAndDeleteSecret verifies the writable KV-v2 surface.
func TestHandlerWriteListAndDeleteSecret(t *testing.T) {
	store := &memoryStore{}
	handler := NewHandler(store, nil)
	token := loginToken(t, handler, "dev")

	writeReq := httptest.NewRequest(http.MethodPut, "/vault/dev/v1/secret/data/apps/app/db", bytes.NewBufferString(`{"data":{"password":"secret"}}`))
	writeReq.Header.Set("X-Vault-Token", token)
	writeRec := httptest.NewRecorder()
	handler.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write status = %d, body = %s", writeRec.Code, writeRec.Body.String())
	}

	listReq := httptest.NewRequest(methodList, "/vault/dev/v1/secret/metadata/apps/app", nil)
	listReq.Header.Set("X-Vault-Token", token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data.Keys) != 1 || listResp.Data.Keys[0] != "db" {
		t.Fatalf("list keys = %#v, want [db]", listResp.Data.Keys)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/vault/dev/v1/secret/metadata/apps/app/db", nil)
	deleteReq.Header.Set("X-Vault-Token", token)
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}

	readReq := httptest.NewRequest(http.MethodGet, "/vault/dev/v1/secret/data/apps/app/db", nil)
	readReq.Header.Set("X-Vault-Token", token)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusNotFound {
		t.Fatalf("read after delete status = %d, want %d", readRec.Code, http.StatusNotFound)
	}
}

// TestHandlerWithFileStoreWriteReadAndList verifies the HTTP handler works with
// the encrypted file-backed fakevault store used by the local server.
func TestHandlerWithFileStoreWriteReadAndList(t *testing.T) {
	store := NewFileStore(&testKeyring{}, t.TempDir())
	handler := NewHandler(store, nil)
	token := loginToken(t, handler, "dev")

	writeReq := httptest.NewRequest(http.MethodPut, "/vault/dev/v1/secret/data/apps/app/api", bytes.NewBufferString(`{"data":{"token":"secret"}}`))
	writeReq.Header.Set("X-Vault-Token", token)
	writeRec := httptest.NewRecorder()
	handler.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write status = %d, body = %s", writeRec.Code, writeRec.Body.String())
	}

	readReq := httptest.NewRequest(http.MethodGet, "/vault/dev/v1/secret/data/apps/app/api", nil)
	readReq.Header.Set("X-Vault-Token", token)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readRec.Code, readRec.Body.String())
	}
	var readResp struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &readResp); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if got, want := readResp.Data.Data["token"], "secret"; got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}

	listReq := httptest.NewRequest(methodList, "/vault/dev/v1/secret/metadata/apps/app", nil)
	listReq.Header.Set("X-Vault-Token", token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data.Keys) != 1 || listResp.Data.Keys[0] != "api" {
		t.Fatalf("list keys = %#v, want [api]", listResp.Data.Keys)
	}
}

// TestHandlerAcceptsBaoWriteBody verifies flat bao write payloads are accepted.
func TestHandlerAcceptsBaoWriteBody(t *testing.T) {
	store := &memoryStore{}
	handler := NewHandler(store, nil)
	token := loginToken(t, handler, "dev")

	writeReq := httptest.NewRequest(http.MethodPut, "/vault/dev/v1/secret/data/apps/app/db", bytes.NewBufferString(`{"password":"secret"}`))
	writeReq.Header.Set("X-Vault-Token", token)
	writeRec := httptest.NewRecorder()
	handler.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusOK {
		t.Fatalf("write status = %d, body = %s", writeRec.Code, writeRec.Body.String())
	}

	readReq := httptest.NewRequest(http.MethodGet, "/vault/dev/v1/secret/data/apps/app/db", nil)
	readReq.Header.Set("X-Vault-Token", token)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readRec.Code, readRec.Body.String())
	}
	var readResp struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &readResp); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if readResp.Data.Data["password"] != "secret" {
		t.Fatalf("password = %q, want secret", readResp.Data.Data["password"])
	}
}

// TestTrustedHandlerCreateOnlyWrite verifies KV-v2 cas=0 creates once without
// a Vault token and cannot overwrite the winning value.
func TestTrustedHandlerCreateOnlyWrite(t *testing.T) {
	store := &memoryStore{}
	handler := NewTrustedHandler(store)
	path := "/vault/dev/v1/secret/data/dev/workload-ca-key"

	first := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(`{"options":{"cas":0},"data":{"value":"first"}}`))
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first write status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}

	second := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(`{"options":{"cas":0},"data":{"value":"second"}}`))
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusBadRequest {
		t.Fatalf("second write status = %d, want %d", secondRec.Code, http.StatusBadRequest)
	}

	read := httptest.NewRequest(http.MethodGet, path, nil)
	readRec := httptest.NewRecorder()
	handler.ServeHTTP(readRec, read)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readRec.Code, readRec.Body.String())
	}
	var response struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if response.Data.Data["value"] != "first" {
		t.Fatalf("value = %q, want first", response.Data.Data["value"])
	}
}

// TestTrustedHandlerCompareAndSetWrite verifies KV-v2 updates accept the
// current version and reject a stale version without overwriting the value.
func TestTrustedHandlerCompareAndSetWrite(t *testing.T) {
	store := &memoryStore{}
	handler := NewTrustedHandler(store)
	path := "/vault/dev/v1/secret/data/apps/app/api-key"

	create := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(`{"options":{"cas":0},"data":{"value":"first"}}`))
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	update := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(`{"options":{"cas":1},"data":{"value":"second"}}`))
	updateRec := httptest.NewRecorder()
	handler.ServeHTTP(updateRec, update)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}
	var updateResponse struct {
		Data struct {
			Version int `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(updateRec.Body.Bytes(), &updateResponse); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updateResponse.Data.Version != 2 {
		t.Fatalf("update version = %d, want 2", updateResponse.Data.Version)
	}

	stale := httptest.NewRequest(http.MethodPut, path, bytes.NewBufferString(`{"options":{"cas":1},"data":{"value":"stale"}}`))
	staleRec := httptest.NewRecorder()
	handler.ServeHTTP(staleRec, stale)
	if staleRec.Code != http.StatusBadRequest {
		t.Fatalf("stale update status = %d, want %d", staleRec.Code, http.StatusBadRequest)
	}
	values, ok, err := store.GetSecret("dev", "secret/data/apps/app/api-key")
	if err != nil || !ok || values["value"] != "second" {
		t.Fatalf("stored value after stale update = %#v, %v, %v", values, ok, err)
	}
}

// TestClusterDeleteRequiresTrustedHandler verifies cluster-wide cleanup is not
// exposed through the VM-facing authenticated handler.
func TestClusterDeleteRequiresTrustedHandler(t *testing.T) {
	store := &memoryStore{secrets: map[string]map[string]string{
		"dev:secret/data/dev/key": {"value": "secret"},
	}}
	path := "/vault/dev/v1/sys/podplane/cluster"

	untrustedRec := httptest.NewRecorder()
	NewHandler(store, nil).ServeHTTP(untrustedRec, httptest.NewRequest(http.MethodDelete, path, nil))
	if untrustedRec.Code != http.StatusNotFound {
		t.Fatalf("untrusted delete status = %d, want %d", untrustedRec.Code, http.StatusNotFound)
	}

	trustedRec := httptest.NewRecorder()
	NewTrustedHandler(store).ServeHTTP(trustedRec, httptest.NewRequest(http.MethodDelete, path, nil))
	if trustedRec.Code != http.StatusNoContent {
		t.Fatalf("trusted delete status = %d, body = %s", trustedRec.Code, trustedRec.Body.String())
	}
	if len(store.secrets) != 0 {
		t.Fatalf("secrets after cluster delete = %#v", store.secrets)
	}
}

// TestHandlerReturnsKVV2MountMetadata verifies bao KV-v2 mount discovery.
func TestHandlerReturnsKVV2MountMetadata(t *testing.T) {
	handler := NewHandler(&memoryStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/vault/dev/v1/sys/internal/ui/mounts/secret/apps/app/db", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mount status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Path    string            `json:"path"`
			Type    string            `json:"type"`
			Options map[string]string `json:"options"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode mount response: %v", err)
	}
	if resp.Data.Path != "secret/" || resp.Data.Type != "kv" || resp.Data.Options["version"] != "2" {
		t.Fatalf("mount response = %#v", resp.Data)
	}
}

// loginToken logs into the handler and returns the issued fakevault token.
func loginToken(t *testing.T, handler http.Handler, clusterID string) string {
	t.Helper()
	loginReq := httptest.NewRequest(http.MethodPut, "/vault/"+clusterID+"/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"app"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return loginResp.Auth.ClientToken
}
