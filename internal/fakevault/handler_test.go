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
	secrets map[string]map[string]string
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
	s.secrets[clusterID+":"+CleanPath(path)] = values
	return nil
}

// GetSecret returns a fakevault secret from memory for handler tests.
func (s *memoryStore) GetSecret(clusterID, path string) (map[string]string, bool, error) {
	values, ok := s.secrets[clusterID+":"+CleanPath(path)]
	return values, ok, nil
}

// DeleteSecret removes a fakevault secret from memory for handler tests.
func (s *memoryStore) DeleteSecret(clusterID, path string) error {
	delete(s.secrets, clusterID+":"+CleanPath(path))
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
		secrets = append(secrets, Secret{Path: path, Keys: sortedKeys(values)})
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
