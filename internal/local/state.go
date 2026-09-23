// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// clusterState is the local cluster VM metadata shared by CLI
// commands and the shared local ingress server.
type clusterState struct {
	ClusterID string    `json:"cluster_id"`
	Backend   string    `json:"backend"`
	Ports     portState `json:"ports"`
}

// portState is the selected host port set for one local cluster VM.
type portState struct {
	SSH           int `json:"ssh"`
	KubernetesAPI int `json:"kubernetes_api"`
	Registry      int `json:"registry"`
	IngressHTTPS  int `json:"ingress_https"`
}

// writeState writes state metadata for one local
// cluster.
func writeState(runtimeDir string, state clusterState) error {
	path := statePath(runtimeDir, state.ClusterID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// readState reads state metadata for one local
// cluster.
func readState(runtimeDir, clusterID string) (clusterState, error) {
	var state clusterState
	data, err := os.ReadFile(statePath(runtimeDir, clusterID))
	if err != nil {
		return state, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("unmarshal state: %w", err)
	}
	return state, nil
}

// removeState removes state metadata for one local
// cluster.
func removeState(runtimeDir, clusterID string) error {
	err := os.Remove(statePath(runtimeDir, clusterID))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove state: %w", err)
}

// statePath returns the state metadata path for one local
// cluster.
func statePath(runtimeDir, clusterID string) string {
	return filepath.Join(runtimeDir, "local", clusterID+".state.json")
}
