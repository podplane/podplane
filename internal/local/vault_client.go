// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// localVaultURL supplies the HTTP authority required by requests transported
// over the local Vault Unix socket.
const localVaultURL = "http://podplane.local"

// localVaultClient accesses the trusted fake Vault API over its user-only Unix
// socket.
type localVaultClient struct {
	http *http.Client
}

// newLocalVaultClient creates a client for the local server's Vault socket.
func newLocalVaultClient(socketPath string) *localVaultClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &localVaultClient{http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		}},
	}}
}

// GetSecret returns one secret from the local fake Vault KV-v2 API.
func (c *localVaultClient) GetSecret(clusterID, path string) (map[string]string, bool, error) {
	request, err := http.NewRequest(http.MethodGet, localVaultURL+vaultDataPath(clusterID, path), nil)
	if err != nil {
		return nil, false, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("read local Vault secret: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, false, vaultResponseError(response)
	}
	var body struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil, false, fmt.Errorf("decode local Vault response: %w", err)
	}
	return body.Data.Data, true, nil
}

// CreateSecret creates one secret through the local fake Vault KV-v2 API. It
// reports false when a value already exists at path.
func (c *localVaultClient) CreateSecret(clusterID, path string, values map[string]string) (bool, error) {
	payload, err := json.Marshal(map[string]any{
		"options": map[string]int{"cas": 0},
		"data":    values,
	})
	if err != nil {
		return false, fmt.Errorf("encode local Vault secret: %w", err)
	}
	request, err := http.NewRequest(http.MethodPut, localVaultURL+vaultDataPath(clusterID, path), bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return false, fmt.Errorf("create local Vault secret: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusOK {
		return true, nil
	}
	responseErr := vaultResponseError(response)
	if response.StatusCode == http.StatusBadRequest && strings.Contains(responseErr.Error(), "check-and-set parameter did not match") {
		return false, nil
	}
	return false, responseErr
}

// DeleteCluster removes all fake Vault state for clusterID.
func (c *localVaultClient) DeleteCluster(clusterID string) error {
	request, err := http.NewRequest(http.MethodDelete, localVaultURL+"/vault/"+clusterID+"/v1/sys/podplane/cluster", nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("delete local Vault cluster: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return vaultResponseError(response)
	}
	return nil
}

// vaultDataPath returns the cluster-scoped KV-v2 data API path.
func vaultDataPath(clusterID, path string) string {
	path = strings.TrimPrefix(strings.Trim(path, "/"), "v1/")
	return "/vault/" + clusterID + "/v1/" + path
}

// vaultResponseError converts a Vault error response into a bounded error.
func vaultResponseError(response *http.Response) error {
	var body struct {
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&body); err == nil && len(body.Errors) > 0 {
		return fmt.Errorf("local Vault returned %s: %s", response.Status, strings.Join(body.Errors, "; "))
	}
	return fmt.Errorf("local Vault returned %s", response.Status)
}
