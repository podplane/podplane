// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

const (
	workloadCAKeyName        = "workload-ca-key"
	workloadCAOwnershipLabel = "podplane-workload-ca-key"
)

// workloadCAKeyOptions identifies the provider and location for a workload CA key.
type workloadCAKeyOptions struct {
	Provider  string
	KeyPrefix string
	Region    string
	Profile   string
	Project   string
}

// workloadCAKeySecret validates options and returns the canonical cloud secret.
func workloadCAKeySecret(opts workloadCAKeyOptions) (cloudSecretOptions, error) {
	prefix := strings.Trim(opts.KeyPrefix, "/")
	if prefix == "" || prefix != opts.KeyPrefix || strings.Contains(prefix, "//") {
		return cloudSecretOptions{}, fmt.Errorf("key_prefix must contain a canonical non-empty provider key prefix")
	}
	switch opts.Provider {
	case cloudSecretProviderAWSSecretsManager:
		return cloudSecretOptions{Provider: opts.Provider, Name: "/" + prefix + "/" + workloadCAKeyName, Region: opts.Region, Profile: opts.Profile}, nil
	case cloudSecretProviderAWSSSM:
		return cloudSecretOptions{Provider: opts.Provider, Name: "/" + prefix + "/" + workloadCAKeyName, Region: opts.Region, Profile: opts.Profile}, nil
	case cloudSecretProviderGCPSecretManager:
		if opts.Project == "" {
			return cloudSecretOptions{}, fmt.Errorf("project is required for google-secret-manager")
		}
		if strings.Contains(prefix, "/") {
			return cloudSecretOptions{}, fmt.Errorf("GCP key_prefix must not contain a slash")
		}
		for _, c := range prefix {
			valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
			if !valid {
				return cloudSecretOptions{}, fmt.Errorf("GCP key_prefix may contain only letters, digits, hyphens, and underscores")
			}
		}
		return cloudSecretOptions{
			Provider:  opts.Provider,
			Name:      "projects/" + opts.Project + "/secrets/" + prefix + "_" + workloadCAKeyName,
			Project:   opts.Project,
			GCPLabels: map[string]string{workloadCAOwnershipLabel: "true"},
		}, nil
	default:
		return cloudSecretOptions{}, fmt.Errorf("provider must be one of aws-secrets-manager, aws-parameter-store, or google-secret-manager")
	}
}

// generateWorkloadCAKey returns a PEM-encoded PKCS#8 Ed25519 private key.
func generateWorkloadCAKey() ([]byte, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Ed25519 key: %w", err)
	}
	defer clear(key)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal PKCS#8 key: %w", err)
	}
	defer clear(der)
	value := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return value, nil
}

// workloadCAKeyFingerprint validates a workload CA key and fingerprints its public key.
func workloadCAKeyFingerprint(value []byte) (string, error) {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return "", fmt.Errorf("value must contain exactly one unencrypted PKCS#8 private key PEM block")
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse PKCS#8 private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return "", fmt.Errorf("PKCS#8 private key is not Ed25519")
	}
	defer clear(key)
	publicDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	sum := sha256.Sum256(publicDER)
	return hex.EncodeToString(sum[:]), nil
}
