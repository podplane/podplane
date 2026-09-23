// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"strings"
	"testing"
)

// memoryWorkloadCAStore stores workload CA values for local bootstrap tests.
type memoryWorkloadCAStore struct {
	values map[string]map[string]string
}

// GetSecret returns an in-memory workload CA secret.
func (s *memoryWorkloadCAStore) GetSecret(clusterID, path string) (map[string]string, bool, error) {
	value, ok := s.values[clusterID+":"+path]
	if !ok {
		return nil, false, nil
	}
	return value, true, nil
}

// CreateSecret creates an in-memory workload CA secret once.
func (s *memoryWorkloadCAStore) CreateSecret(clusterID, path string, values map[string]string) (bool, error) {
	if s.values == nil {
		s.values = make(map[string]map[string]string)
	}
	key := clusterID + ":" + path
	if _, ok := s.values[key]; ok {
		return false, nil
	}
	s.values[key] = values
	return true, nil
}

// TestEnsureWorkloadCAKeyCreatesAndPreservesKey verifies local restarts do not
// rotate the workload CA key.
func TestEnsureWorkloadCAKeyCreatesAndPreservesKey(t *testing.T) {
	store := &memoryWorkloadCAStore{}
	if err := ensureWorkloadCAKey(store, "dev", true); err != nil {
		t.Fatalf("ensureWorkloadCAKey: %v", err)
	}
	path := "secret/data/dev/workload-ca-key"
	first, ok, err := store.GetSecret("dev", path)
	if err != nil || !ok {
		t.Fatalf("GetSecret after create = %v, %v", ok, err)
	}
	if err := validateWorkloadCAKey([]byte(first["value"])); err != nil {
		t.Fatalf("stored key: %v", err)
	}
	if err := ensureWorkloadCAKey(store, "dev", false); err != nil {
		t.Fatalf("ensureWorkloadCAKey on restart: %v", err)
	}
	second, ok, err := store.GetSecret("dev", path)
	if err != nil || !ok {
		t.Fatalf("GetSecret after restart = %v, %v", ok, err)
	}
	if first["value"] != second["value"] {
		t.Fatal("ensureWorkloadCAKey rotated an existing key")
	}
}

// TestEnsureWorkloadCAKeyRejectsExistingClusterWithoutKey verifies an old local
// cluster is not silently assigned a CA its existing seed does not reference.
func TestEnsureWorkloadCAKeyRejectsExistingClusterWithoutKey(t *testing.T) {
	store := &memoryWorkloadCAStore{}
	err := ensureWorkloadCAKey(store, "dev", false)
	if err == nil || !strings.Contains(err.Error(), "delete and recreate") {
		t.Fatalf("ensureWorkloadCAKey error = %v, want recreation instruction", err)
	}
}
