// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// memoryStore tracks secret values, archive state, and versions for handler tests.
type memoryStore struct {
	secrets  map[string]map[string]string
	archived map[string]bool
	versions map[string]int
}

// rejectToken always rejects login JWTs for handler tests.
func rejectToken(context.Context, string, string, string) (Identity, error) {
	return Identity{}, fmt.Errorf("rejected")
}

func managementToken(context.Context, string, string, string) (Identity, error) {
	return Identity{Namespace: operatorServiceAccountNamespace, ServiceAccount: operatorServiceAccountName, ExpiresAt: time.Now().Add(time.Hour)}, nil
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

// TestHandlerLoginAndReadSecret verifies a signed operator JWT grants cluster-wide reads.
func TestHandlerLoginAndReadSecret(t *testing.T) {
	store := &memoryStore{secrets: map[string]map[string]string{
		"dev:secret/data/apps/app/db": {"password": "secret"},
	}}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	validator := testKubernetesTokenValidator(t, key)
	handler := NewHandler(store, validator.ValidateIdentity)
	rawToken, err := testServiceAccountJWTForSubject(key, "system:serviceaccount:platform-podplane-operator:platform-podplane-operator")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := loginWithJWT(t, handler, "dev", "podplane-operator", rawToken)

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

// TestHandlerRejectsCrossClusterToken verifies issued tokens stay cluster-scoped.
func TestHandlerRejectsCrossClusterToken(t *testing.T) {
	store := &memoryStore{secrets: map[string]map[string]string{
		"other:secret/data/apps/app/db": {"password": "secret"},
	}}
	handler := NewHandler(store, managementToken)

	loginReq := httptest.NewRequest(http.MethodPost, "/vault/dev/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"podplane-operator"}`))
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
	handler := NewHandler(store, managementToken)

	loginReq := httptest.NewRequest(http.MethodPost, "/vault/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"app"}`))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusNotFound {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusNotFound)
	}
}

// TestHandlerRejectsInvalidLoginJWT verifies missing or failing validators reject login.
func TestHandlerRejectsInvalidLoginJWT(t *testing.T) {
	for _, tt := range []struct {
		name      string
		validator func(context.Context, string, string, string) (Identity, error)
	}{
		{name: "missing validator"},
		{name: "rejected JWT", validator: rejectToken},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(&memoryStore{}, tt.validator)
			loginReq := httptest.NewRequest(http.MethodPost, "/vault/dev/v1/auth/kubernetes/login", bytes.NewBufferString(`{"jwt":"token","role":"app"}`))
			loginRec := httptest.NewRecorder()
			handler.ServeHTTP(loginRec, loginReq)
			if loginRec.Code != http.StatusForbidden {
				t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestHandlerWorkloadAuthorization exercises signed JWT login through scoped KV access.
func TestHandlerWorkloadAuthorization(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	validator := testKubernetesTokenValidator(t, key)
	rawToken, err := testServiceAccountJWTForSubject(key, "system:serviceaccount:team-a:api")
	if err != nil {
		t.Fatal(err)
	}
	const ownPath = "secret/data/dev/team-a/api/key"
	for _, tt := range []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"own secret", http.MethodGet, "/vault/dev/v1/" + ownPath, http.StatusOK},
		{"other namespace", http.MethodGet, "/vault/dev/v1/secret/data/dev/team-b/api/key", http.StatusForbidden},
		{"other binding", http.MethodGet, "/vault/dev/v1/secret/data/dev/team-a/worker/key", http.StatusForbidden},
		{"binding prefix", http.MethodGet, "/vault/dev/v1/secret/data/dev/team-a/api-other/key", http.StatusForbidden},
		{"other cluster", http.MethodGet, "/vault/other/v1/" + ownPath, http.StatusForbidden},
		{"other key prefix", http.MethodGet, "/vault/dev/v1/secret/data/other/team-a/api/key", http.StatusForbidden},
		{"CA key", http.MethodGet, "/vault/dev/v1/secret/data/dev/workload-ca-key", http.StatusForbidden},
		{"parent traversal", http.MethodGet, "/vault/dev/v1/secret/data/dev/team-a/api/../../worker/key", http.StatusForbidden},
		{"encoded traversal", http.MethodGet, "/vault/dev/v1/secret/data/dev/team-a/api/%2e%2e/worker/key", http.StatusForbidden},
		{"empty segment", http.MethodGet, "/vault/dev/v1/secret/data/dev/team-a/api//key", http.StatusForbidden},
		{"metadata", http.MethodGet, "/vault/dev/v1/secret/metadata/dev/team-a/api/key", http.StatusForbidden},
		{"list query", http.MethodGet, "/vault/dev/v1/" + ownPath + "?list=true", http.StatusForbidden},
		{"list method", methodList, "/vault/dev/v1/secret/metadata/dev/team-a/api", http.StatusForbidden},
		{"post", http.MethodPost, "/vault/dev/v1/" + ownPath, http.StatusForbidden},
		{"put", http.MethodPut, "/vault/dev/v1/" + ownPath, http.StatusForbidden},
		{"patch", http.MethodPatch, "/vault/dev/v1/" + ownPath, http.StatusForbidden},
		{"archive", http.MethodDelete, "/vault/dev/v1/" + ownPath, http.StatusForbidden},
		{"delete metadata", http.MethodDelete, "/vault/dev/v1/secret/metadata/dev/team-a/api/key", http.StatusForbidden},
		{"restore", http.MethodPost, "/vault/dev/v1/secret/undelete/dev/team-a/api/key", http.StatusForbidden},
		{"destroy", http.MethodPost, "/vault/dev/v1/secret/destroy/dev/team-a/api/key", http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewFileStore(&testKeyring{}, t.TempDir())
			for _, path := range []string{ownPath, "secret/data/dev/team-b/api/key", "secret/data/dev/team-a/worker/key", "secret/data/dev/workload-ca-key"} {
				if err := store.SetSecret("dev", path, map[string]string{"value": "original"}); err != nil {
					t.Fatal(err)
				}
			}
			handler := NewHandler(store, validator.ValidateIdentity)
			token, _ := loginWithJWT(t, handler, "dev", "api", rawToken)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"data":{"value":"changed"},"versions":[1]}`))
			req.Header.Set("X-Vault-Token", token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.status == http.StatusOK {
				var response struct {
					Data struct {
						Data map[string]string `json:"data"`
					} `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Data.Data["value"] != "original" {
					t.Fatalf("read value = %q, want original", response.Data.Data["value"])
				}
			}
			values, ok, err := store.GetSecret("dev", ownPath)
			if err != nil || !ok || values["value"] != "original" {
				t.Fatalf("stored value changed: %v, exists=%v, error=%v", values, ok, err)
			}
		})
	}
}

// TestHandlerTokenExpiration checks both lifetime limits without waiting on wall time.
func TestHandlerTokenExpiration(t *testing.T) {
	for _, tt := range []struct {
		name     string
		jwtTTL   time.Duration
		tokenTTL time.Duration
	}{
		{"JWT limit", 10 * time.Minute, 10 * time.Minute},
		{"one hour limit", 4 * time.Hour, time.Hour},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				identity := Identity{Namespace: "team-a", ServiceAccount: "api", ExpiresAt: time.Now().Add(tt.jwtTTL)}
				store := &memoryStore{secrets: map[string]map[string]string{
					"dev:secret/data/dev/team-a/api/key": {"value": "original"},
				}}
				handler := NewHandler(store, func(context.Context, string, string, string) (Identity, error) {
					return identity, nil
				})
				token, lease := loginWithJWT(t, handler, "dev", "api", "verified-by-test-validator")
				if lease != tt.tokenTTL {
					t.Fatalf("lease = %s, want %s", lease, tt.tokenTTL)
				}
				time.Sleep(tt.tokenTTL - time.Nanosecond)
				for _, want := range []int{http.StatusOK, http.StatusForbidden} {
					req := httptest.NewRequest(http.MethodGet, "/vault/dev/v1/secret/data/dev/team-a/api/key", nil)
					req.Header.Set("X-Vault-Token", token)
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					if rec.Code != want {
						t.Fatalf("status at %s = %d, want %d", time.Now(), rec.Code, want)
					}
					time.Sleep(time.Nanosecond)
				}
			})
		})
	}
}

// TestHandlerWriteListAndDeleteSecret verifies the writable KV-v2 surface.
func TestHandlerWriteListAndDeleteSecret(t *testing.T) {
	store := &memoryStore{}
	handler := NewHandler(store, managementToken)
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
	handler := NewHandler(store, managementToken)
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
	handler := NewHandler(store, managementToken)
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
	token, _ := loginWithJWT(t, handler, clusterID, "podplane-operator", "token")
	return token
}

// loginWithJWT requires successful login and returns the token and advertised lifetime.
func loginWithJWT(t *testing.T, handler http.Handler, clusterID, role, rawToken string) (string, time.Duration) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"jwt": rawToken, "role": role})
	if err != nil {
		t.Fatal(err)
	}
	loginReq := httptest.NewRequest(http.MethodPut, "/vault/"+clusterID+"/v1/auth/kubernetes/login", bytes.NewReader(body))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Auth.ClientToken == "" {
		t.Fatal("login response missing client token")
	}
	return loginResp.Auth.ClientToken, time.Duration(loginResp.Auth.LeaseDuration) * time.Second
}
