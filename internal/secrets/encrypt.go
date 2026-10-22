// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Algorithm is the supported client-side write encryption algorithm.
const Algorithm = "x25519-hkdf-sha256-aes-256-gcm"

// WriteOptions describes one encrypted SecretProviderKeyspace write request.
type WriteOptions struct {
	Namespace    string
	KeyspaceName string
	ClusterID    string
	Key          string
	Operation    string
	Value        []byte
	Binary       bool
}

// EncryptedRequest builds an encrypted SecretProviderKeyspace write request.
func (c Client) EncryptedRequest(opts WriteOptions) (SecretProviderKeyspace, error) {
	if !opts.Binary && !utf8.Valid(opts.Value) {
		return SecretProviderKeyspace{}, fmt.Errorf("secret value must be valid UTF-8 text")
	}
	pub, err := c.PublicKey()
	if err != nil {
		return SecretProviderKeyspace{}, err
	}
	if pub.Spec.Algorithm != Algorithm {
		return SecretProviderKeyspace{}, fmt.Errorf("operator public key algorithm %q is not supported", pub.Spec.Algorithm)
	}
	ciphertext, err := EncryptWriteValue(pub.Spec.PublicKey, opts.Value, AssociatedData(opts))
	if err != nil {
		return SecretProviderKeyspace{}, err
	}
	request := NewKeyspaceRequest(opts.Namespace, opts.KeyspaceName, opts.Key, opts.Operation, &EncryptedValue{
		KeyID:      pub.Spec.KeyID,
		Algorithm:  Algorithm,
		Ciphertext: ciphertext,
	})
	request.Spec.Entries[0].Binary = opts.Binary
	return request, nil
}

// PutEncrypted sends an encrypted write request and retries once after key rotation.
func (c Client) PutEncrypted(request SecretProviderKeyspace, opts WriteOptions) (SecretProviderKeyspace, error) {
	response, err := c.Put(request)
	if err != nil && IsStalePublicKeyError(err) {
		request, err = c.EncryptedRequest(opts)
		if err == nil {
			response, err = c.Put(request)
		}
	}
	return response, err
}

// AssociatedData binds the secret's identity and binary mode to its ciphertext.
func AssociatedData(opts WriteOptions) []byte {
	return []byte(strings.Join([]string{
		Algorithm,
		opts.ClusterID,
		opts.Namespace,
		opts.KeyspaceName,
		opts.Key,
		strconv.FormatBool(opts.Binary),
	}, "\x00"))
}

// encryptedSecretEnvelope is the versioned wire format embedded in encrypted write requests.
type encryptedSecretEnvelope struct {
	Version            string `json:"version"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

// EncryptWriteValue encrypts one secret write value for the operator public key.
func EncryptWriteValue(recipient string, plaintext, associatedData []byte) (string, error) {
	recipientKeyBytes, err := DecodePublicKey(recipient)
	if err != nil {
		return "", fmt.Errorf("decode operator public key: %w", err)
	}
	recipientKey, err := ecdh.X25519().NewPublicKey(recipientKeyBytes)
	if err != nil {
		return "", fmt.Errorf("parse operator public key: %w", err)
	}
	ephemeralKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ephemeral encryption key: %w", err)
	}
	sharedSecret, err := ephemeralKey.ECDH(recipientKey)
	if err != nil {
		return "", fmt.Errorf("derive shared secret: %w", err)
	}
	salt := ephemeralKey.PublicKey().Bytes()
	aeadKey, err := hkdf.Key(sha256.New, sharedSecret, salt, "podplane secrets v1", 32)
	if err != nil {
		return "", fmt.Errorf("derive content encryption key: %w", err)
	}
	block, err := aes.NewCipher(aeadKey)
	if err != nil {
		return "", fmt.Errorf("initialize AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("initialize AES-GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	envelope := encryptedSecretEnvelope{
		Version:            "podplane-secrets-v1",
		EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeralKey.PublicKey().Bytes()),
		Nonce:              base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, nonce, plaintext, associatedData)),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode encrypted secret envelope: %w", err)
	}
	return "podplane-secrets-v1." + base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodePublicKey accepts padded standard base64 or unpadded base64url public keys.
func DecodePublicKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("must be base64-encoded raw X25519 public key bytes")
}

// IsStalePublicKeyError reports whether an API error indicates key rotation.
func IsStalePublicKeyError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "stale public key") && strings.Contains(msg, "conflict")
}
