// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const methodList = "LIST"

type handler struct {
	store     Store
	validator func(context.Context, string, string, string) error
	trusted   bool
	mu        sync.Mutex
	tokens    map[string]token
}

// NewTrustedHandler returns a Vault/OpenBao-compatible handler for a
// user-protected local transport. Requests do not require Vault tokens.
func NewTrustedHandler(store Store) http.Handler {
	return &handler{store: store, trusted: true, tokens: make(map[string]token)}
}

type token struct {
	ClusterID string
	ExpiresAt time.Time
}

type loginRequest struct {
	JWT  string `json:"jwt"`
	Role string `json:"role"`
}

// NewHandler returns a minimal Vault/OpenBao-compatible HTTP handler. When
// validator is set, Kubernetes auth login JWTs must pass validation.
func NewHandler(store Store, validator func(context.Context, string, string, string) error) http.Handler {
	return &handler{
		store:     store,
		validator: validator,
		tokens:    make(map[string]token),
	}
}

// ServeHTTP routes Vault/OpenBao-compatible API requests to the fakevault handler.
func (h *handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	clusterID, apiPath, ok := apiPath(r.URL.Path)
	if !ok {
		errorResponse(rw, http.StatusNotFound, "not found")
		return
	}
	if strings.HasPrefix(apiPath, "/auth/") && strings.HasSuffix(apiPath, "/login") {
		h.serveLogin(rw, r, clusterID)
		return
	}
	if strings.HasPrefix(apiPath, "/sys/internal/ui/mounts/") {
		h.serveMount(rw, r)
		return
	}
	if apiPath == "/sys/podplane/cluster" {
		h.serveClusterDelete(rw, r, clusterID)
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		if path, ok := undeleteDataPath(apiPath); ok {
			h.serveUndelete(rw, r, clusterID, path)
			return
		}
		if path, ok := destroyDataPath(apiPath); ok {
			h.serveDestroy(rw, r, clusterID, path)
			return
		}
	}
	h.serveKV(rw, r, clusterID, apiPath)
}

// serveLogin handles the Kubernetes auth login endpoint used by the CSI provider.
func (h *handler) serveLogin(rw http.ResponseWriter, r *http.Request, clusterID string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		rw.Header().Set("Allow", http.MethodPost+", "+http.MethodPut)
		errorResponse(rw, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(rw, http.StatusBadRequest, "invalid login request")
		return
	}
	if strings.TrimSpace(req.Role) == "" {
		errorResponse(rw, http.StatusBadRequest, "role is required")
		return
	}
	if strings.TrimSpace(req.JWT) == "" {
		errorResponse(rw, http.StatusBadRequest, "jwt is required")
		return
	}
	if h.validator != nil {
		if err := h.validator(r.Context(), clusterID, req.Role, req.JWT); err != nil {
			errorResponse(rw, http.StatusForbidden, "invalid jwt")
			return
		}
	}

	clientToken, err := randomToken()
	if err != nil {
		errorResponse(rw, http.StatusInternalServerError, "create token")
		return
	}
	expiresAt := time.Now().Add(time.Hour)
	h.mu.Lock()
	h.tokens[clientToken] = token{ClusterID: clusterID, ExpiresAt: expiresAt}
	h.mu.Unlock()

	jsonResponse(rw, http.StatusOK, map[string]any{
		"auth": map[string]any{
			"client_token":   clientToken,
			"lease_duration": int(time.Hour.Seconds()),
			"renewable":      false,
		},
	})
}

// serveMount returns enough mount metadata for the bao CLI to detect KV-v2.
func (h *handler) serveMount(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		rw.Header().Set("Allow", http.MethodGet)
		errorResponse(rw, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jsonResponse(rw, http.StatusOK, map[string]any{
		"data": map[string]any{
			"path":    "secret/",
			"type":    "kv",
			"options": map[string]string{"version": "2"},
		},
	})
}

// serveKV handles the minimal KV-v2 API surface fakevault supports.
func (h *handler) serveKV(rw http.ResponseWriter, r *http.Request, clusterID, apiPath string) {
	clusterID, ok := h.authorize(rw, r, clusterID)
	if !ok {
		return
	}

	if r.Method == methodList || (r.Method == http.MethodGet && r.URL.Query().Get("list") == "true") {
		h.serveList(rw, clusterID, apiPath)
		return
	}
	if r.Method == http.MethodDelete {
		h.serveDelete(rw, clusterID, apiPath)
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		h.serveWrite(rw, r, clusterID, apiPath)
		return
	}
	if r.Method == http.MethodGet {
		h.serveRead(rw, clusterID, apiPath)
		return
	}

	rw.Header().Set("Allow", strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, methodList}, ", "))
	errorResponse(rw, http.StatusMethodNotAllowed, "method not allowed")
}

// serveRead returns a KV-v2 shaped secret response.
func (h *handler) serveRead(rw http.ResponseWriter, clusterID, apiPath string) {
	if path, ok := metadataDataPath(apiPath); ok {
		h.serveMetadata(rw, clusterID, path)
		return
	}
	path, ok := dataPath(apiPath)
	if !ok {
		errorResponse(rw, http.StatusNotFound, "not found")
		return
	}
	values, ok, err := h.store.GetSecret(clusterID, path)
	if err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		errorResponse(rw, http.StatusNotFound, "secret not found")
		return
	}
	jsonResponse(rw, http.StatusOK, map[string]any{
		"data": map[string]any{
			"data": values,
			"metadata": map[string]any{
				"version": secretVersion(clusterID, path, h.store),
			},
		},
	})
}

// serveWrite stores a KV-v2 secret data request.
func (h *handler) serveWrite(rw http.ResponseWriter, r *http.Request, clusterID, apiPath string) {
	path, ok := dataPath(apiPath)
	if !ok {
		errorResponse(rw, http.StatusNotFound, "not found")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errorResponse(rw, http.StatusBadRequest, "invalid secret data")
		return
	}
	data := body
	nested, nestedData := body["data"].(map[string]any)
	if nestedData {
		data = nested
	}
	create := false
	if rawOptions, hasOptions := body["options"]; hasOptions {
		options, ok := rawOptions.(map[string]any)
		if !ok || !nestedData {
			errorResponse(rw, http.StatusBadRequest, "invalid check-and-set options")
			return
		}
		rawCAS, ok := options["cas"]
		if !ok {
			errorResponse(rw, http.StatusBadRequest, "cas is required")
			return
		}
		cas, ok := rawCAS.(float64)
		if !ok || cas != 0 {
			errorResponse(rw, http.StatusBadRequest, "only cas=0 is supported")
			return
		}
		create = true
	}
	if len(data) == 0 {
		errorResponse(rw, http.StatusBadRequest, "secret data is required")
		return
	}
	values := make(map[string]string, len(data))
	for key, value := range data {
		if key == "" {
			errorResponse(rw, http.StatusBadRequest, "secret data keys must be non-empty")
			return
		}
		if str, ok := value.(string); ok {
			values[key] = str
		} else {
			values[key] = fmt.Sprint(value)
		}
	}
	if create {
		created, err := h.store.CreateSecret(clusterID, path, values)
		if err != nil {
			errorResponse(rw, http.StatusInternalServerError, err.Error())
			return
		}
		if !created {
			errorResponse(rw, http.StatusBadRequest, "check-and-set parameter did not match the current version")
			return
		}
	} else {
		if err := h.store.SetSecret(clusterID, path, values); err != nil {
			errorResponse(rw, http.StatusInternalServerError, err.Error())
			return
		}
	}
	jsonResponse(rw, http.StatusOK, map[string]any{
		"data": map[string]any{
			"version": secretVersion(clusterID, path, h.store),
		},
	})
}

// serveClusterDelete removes all fakevault state for a local cluster. It is
// available only through the trusted local transport.
func (h *handler) serveClusterDelete(rw http.ResponseWriter, r *http.Request, clusterID string) {
	if !h.trusted {
		errorResponse(rw, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodDelete {
		rw.Header().Set("Allow", http.MethodDelete)
		errorResponse(rw, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := h.store.DeleteCluster(clusterID); err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(rw, http.StatusNoContent, nil)
}

// serveDelete soft-deletes a KV-v2 data path or permanently deletes metadata.
func (h *handler) serveDelete(rw http.ResponseWriter, clusterID, apiPath string) {
	if path, ok := metadataDataPath(apiPath); ok {
		if err := h.store.DeleteSecret(clusterID, path); err != nil {
			errorResponse(rw, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(rw, http.StatusNoContent, nil)
		return
	}
	path, ok := dataPath(apiPath)
	if !ok {
		errorResponse(rw, http.StatusNotFound, "not found")
		return
	}
	if err := h.store.ArchiveSecret(clusterID, path); err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(rw, http.StatusNoContent, nil)
}

// serveList returns a KV-v2 metadata list response.
func (h *handler) serveList(rw http.ResponseWriter, clusterID, apiPath string) {
	prefix, ok := metadataDataPath(apiPath)
	if !ok {
		errorResponse(rw, http.StatusNotFound, "not found")
		return
	}
	secrets, err := h.store.ListSecrets(clusterID)
	if err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	keys := map[string]bool{}
	prefix = strings.Trim(prefix, "/")
	matchPrefix := prefix + "/"
	for _, secret := range secrets {
		path := strings.Trim(secret.Path, "/")
		if !strings.HasPrefix(path, matchPrefix) {
			continue
		}
		rel := strings.TrimPrefix(path, matchPrefix)
		name, rest, _ := strings.Cut(rel, "/")
		if rest != "" {
			name += "/"
		}
		if name != "" {
			keys[name] = true
		}
	}
	if len(keys) == 0 {
		errorResponse(rw, http.StatusNotFound, "secret not found")
		return
	}
	list := make([]string, 0, len(keys))
	for key := range keys {
		list = append(list, key)
	}
	sort.Strings(list)
	jsonResponse(rw, http.StatusOK, map[string]any{"data": map[string]any{"keys": list}})
}

// serveMetadata returns enough KV-v2 metadata for adapters to detect archived state.
func (h *handler) serveMetadata(rw http.ResponseWriter, clusterID, path string) {
	secrets, err := h.store.ListSecrets(clusterID)
	if err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	path = strings.Trim(path, "/")
	for _, secret := range secrets {
		if strings.Trim(secret.Path, "/") != path {
			continue
		}
		version := secret.Version
		if version <= 0 {
			version = 1
		}
		deletedAt := ""
		if secret.Archived {
			deletedAt = time.Now().UTC().Format(time.RFC3339)
		}
		jsonResponse(rw, http.StatusOK, map[string]any{"data": map[string]any{
			"current_version": version,
			"versions": map[string]any{
				fmt.Sprint(version): map[string]any{"deletion_time": deletedAt, "destroyed": false},
			},
		}})
		return
	}
	errorResponse(rw, http.StatusNotFound, "secret not found")
}

// serveUndelete restores an archived KV-v2 data path.
func (h *handler) serveUndelete(rw http.ResponseWriter, r *http.Request, clusterID, path string) {
	clusterID, ok := h.authorize(rw, r, clusterID)
	if !ok {
		return
	}
	if err := h.store.RestoreSecret(clusterID, path); err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(rw, http.StatusNoContent, nil)
}

// serveDestroy permanently deletes a KV-v2 data path.
func (h *handler) serveDestroy(rw http.ResponseWriter, r *http.Request, clusterID, path string) {
	clusterID, ok := h.authorize(rw, r, clusterID)
	if !ok {
		return
	}
	if err := h.store.DeleteSecret(clusterID, path); err != nil {
		errorResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(rw, http.StatusNoContent, nil)
}

// authorize returns the request's cluster after enforcing token authentication
// on the VM-facing transport. The trusted local transport relies on its socket
// permissions instead.
func (h *handler) authorize(rw http.ResponseWriter, r *http.Request, clusterID string) (string, bool) {
	if h.trusted {
		return clusterID, true
	}
	entry, ok := h.authenticate(r.Header.Get("X-Vault-Token"), clusterID)
	if !ok {
		errorResponse(rw, http.StatusForbidden, "permission denied")
		return "", false
	}
	return entry.ClusterID, true
}

// secretVersion returns the stored version for path, defaulting to one when unknown.
func secretVersion(clusterID, path string, store Store) int {
	secrets, err := store.ListSecrets(clusterID)
	if err != nil {
		return 1
	}
	path = strings.Trim(path, "/")
	for _, secret := range secrets {
		if strings.Trim(secret.Path, "/") == path && secret.Version > 0 {
			return secret.Version
		}
	}
	return 1
}

// authenticate returns a token entry only when it is scoped to clusterID.
func (h *handler) authenticate(clientToken, clusterID string) (token, bool) {
	entry, ok := h.tokenEntry(clientToken)
	return entry, ok && entry.ClusterID == clusterID
}

// tokenEntry returns a non-expired token entry for a client token.
func (h *handler) tokenEntry(clientToken string) (token, bool) {
	if clientToken == "" {
		return token{}, false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.tokens[clientToken]
	if !ok {
		return token{}, false
	}
	if !entry.ExpiresAt.After(now) {
		delete(h.tokens, clientToken)
		return token{}, false
	}
	return entry, true
}

// apiPath extracts the local cluster ID and Vault API path from a request path.
func apiPath(path string) (clusterID, pathWithoutV1 string, ok bool) {
	path = strings.TrimPrefix(path, "/vault/")
	path = strings.Trim(path, "/")
	if path == "" {
		return "", "", false
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[1], "v1/") {
		return "", "", false
	}
	clusterID = parts[0]
	pathWithoutV1 = strings.TrimPrefix(parts[1], "v1")
	if clusterID == "" || pathWithoutV1 == "" {
		return "", "", false
	}
	return clusterID, pathWithoutV1, true
}

// dataPath returns the stored path for a KV-v2 data API path.
func dataPath(apiPath string) (string, bool) {
	parts := strings.SplitN(strings.Trim(apiPath, "/"), "/", 3)
	if len(parts) != 3 || parts[1] != "data" || parts[2] == "" {
		return "", false
	}
	return parts[0] + "/data/" + parts[2], true
}

// metadataDataPath returns the corresponding data path for a KV-v2 metadata API path.
func metadataDataPath(apiPath string) (string, bool) {
	parts := strings.SplitN(strings.Trim(apiPath, "/"), "/", 3)
	if len(parts) < 2 || parts[1] != "metadata" {
		return "", false
	}
	path := parts[0] + "/data"
	if len(parts) == 3 && parts[2] != "" {
		path += "/" + parts[2]
	}
	return path, true
}

// undeleteDataPath returns the stored path for a KV-v2 undelete API path.
func undeleteDataPath(apiPath string) (string, bool) {
	parts := strings.SplitN(strings.Trim(apiPath, "/"), "/", 3)
	if len(parts) != 3 || parts[1] != "undelete" || parts[2] == "" {
		return "", false
	}
	return parts[0] + "/data/" + parts[2], true
}

// destroyDataPath returns the stored path for a KV-v2 destroy API path.
func destroyDataPath(apiPath string) (string, bool) {
	parts := strings.SplitN(strings.Trim(apiPath, "/"), "/", 3)
	if len(parts) != 3 || parts[1] != "destroy" || parts[2] == "" {
		return "", false
	}
	return parts[0] + "/data/" + parts[2], true
}

// randomToken returns an opaque fakevault client token.
func randomToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return "fakevault." + hex.EncodeToString(b[:]), nil
}

// jsonResponse writes a JSON response with the given status code.
func jsonResponse(rw http.ResponseWriter, status int, body any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	if body == nil {
		return
	}
	_ = json.NewEncoder(rw).Encode(body)
}

// errorResponse writes a Vault/OpenBao-style JSON error response.
func errorResponse(rw http.ResponseWriter, status int, message string) {
	jsonResponse(rw, status, map[string]any{"errors": []string{message}})
}
