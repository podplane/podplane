// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

const workloadCAKeyName = "workload-ca-key"

// workloadCAStore is the secret storage required to bootstrap a local workload
// certificate authority.
type workloadCAStore interface {
	GetSecret(clusterID, path string) (map[string]string, bool, error)
	CreateSecret(clusterID, path string, values map[string]string) (bool, error)
}

// ensureWorkloadCAKey creates the local cluster's workload CA key once in its
// encrypted fake Vault store. Existing keys are preserved across restarts.
func ensureWorkloadCAKey(store workloadCAStore, clusterID string, create bool) error {
	if store == nil {
		return fmt.Errorf("local Vault store is required")
	}
	path := fmt.Sprintf("secret/data/%s/%s", clusterID, workloadCAKeyName)
	values, ok, err := store.GetSecret(clusterID, path)
	if err != nil {
		return err
	}
	if ok {
		return validateWorkloadCAKey([]byte(values["value"]))
	}
	if !create {
		return fmt.Errorf("local cluster %q predates workload CA support; delete and recreate it", clusterID)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("encode key: %w", err)
	}
	value := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	created, err := store.CreateSecret(clusterID, path, map[string]string{"value": string(value)})
	if err != nil {
		return fmt.Errorf("store key: %w", err)
	}
	if created {
		return nil
	}
	values, ok, err = store.GetSecret(clusterID, path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workload CA key disappeared during creation")
	}
	return validateWorkloadCAKey([]byte(values["value"]))
}

// validateWorkloadCAKey verifies that an existing local workload CA value is
// an Ed25519 private key in the format consumed by the Podplane operator.
func validateWorkloadCAKey(value []byte) error {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return fmt.Errorf("existing %s is not a PEM PKCS#8 private key", workloadCAKeyName)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse existing %s: %w", workloadCAKeyName, err)
	}
	if _, ok := key.(ed25519.PrivateKey); !ok {
		return fmt.Errorf("existing %s is not an Ed25519 private key", workloadCAKeyName)
	}
	return nil
}
