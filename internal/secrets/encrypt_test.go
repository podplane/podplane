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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncryptedRequestText verifies text and binary request construction,
// authenticated format selection, and preservation across a stale-key retry.
func TestEncryptedRequestText(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := PublicKey{Spec: PublicKeySpec{KeyID: "latest", Algorithm: Algorithm, PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}}
	data, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
touch "$STATE/called"
if [ "$1" = replace ]; then
  cat > "$STATE/request"
  if [ ! -f "$STATE/retried" ]; then
    touch "$STATE/retried"
    echo 'Conflict: stale public key' >&2
    exit 1
  fi
  echo '{}'
  exit 0
fi
cat "$STATE/public"
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STATE", dir)
	opts := WriteOptions{ClusterID: "dev", Namespace: "system", KeyspaceName: "bao.app", Key: "token", Operation: "create", Value: []byte("private\xffvalue")}
	if _, err := (Client{}).EncryptedRequest(opts); err == nil || err.Error() != "secret value must be valid UTF-8 text" {
		t.Fatalf("invalid value error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "called")); !os.IsNotExist(err) {
		t.Fatal("invalid value contacted the cluster")
	}
	for _, value := range []string{"  café �\r\n\t", "-----BEGIN PRIVATE KEY-----\nYWJjZA==\n-----END PRIVATE KEY-----\n", "YWJjZA=="} {
		opts.Value = []byte(value)
		request, err := (Client{}).EncryptedRequest(opts)
		if err != nil {
			t.Fatal(err)
		}
		got := decryptWriteValueForTest(t, key, request.Spec.Entries[0].EncryptedValue.Ciphertext, AssociatedData(opts))
		if string(got) != value {
			t.Fatal("request changed plaintext")
		}
	}
	for _, value := range []string{"\x00\xff\xc3\n", "YWJjZA=="} {
		opts.Value, opts.Binary = []byte(value), true
		request, err := (Client{}).EncryptedRequest(opts)
		if err != nil {
			t.Fatal(err)
		}
		entry := request.Spec.Entries[0]
		if !entry.Binary {
			t.Fatal("request lost binary intent")
		}
		// Expected protocol bytes are independent of AssociatedData's implementation.
		aad := []byte(Algorithm + "\x00dev\x00system\x00bao.app\x00token\x00true")
		got := decryptWriteValueForTest(t, key, entry.EncryptedValue.Ciphertext, aad)
		if string(got) != value {
			t.Fatal("binary plaintext changed or was pre-encoded")
		}
		if _, err := openWriteEnvelopeForTest(key, entry.EncryptedValue.Ciphertext, []byte(Algorithm+"\x00dev\x00system\x00bao.app\x00token\x00false")); err == nil {
			t.Fatal("removing binary intent must invalidate the ciphertext")
		}
		if _, err := (Client{}).PutEncrypted(request, opts); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "request"))
		if err != nil {
			t.Fatal(err)
		}
		var sent SecretProviderKeyspace
		if err := json.Unmarshal(data, &sent); err != nil {
			t.Fatal(err)
		}
		entry = sent.Spec.Entries[0]
		if !entry.Binary || string(decryptWriteValueForTest(t, key, entry.EncryptedValue.Ciphertext, aad)) != value {
			t.Fatal("write/retry changed binary mode or bytes")
		}
	}
}

// TestIsStalePublicKeyErrorRequiresConflictAndMessage verifies exact retry matching.
func TestIsStalePublicKeyErrorRequiresConflictAndMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "stale conflict", err: errInvalidEnvelopeForTest("Conflict: stale public key"), want: true},
		{name: "conflict only", err: errInvalidEnvelopeForTest("Conflict: object changed")},
		{name: "stale only", err: errInvalidEnvelopeForTest("stale public key")},
		{name: "unrelated", err: errInvalidEnvelopeForTest("forbidden")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStalePublicKeyError(tt.err); got != tt.want {
				t.Fatalf("IsStalePublicKeyError(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestAssociatedData verifies that both text and binary writes authenticate their mode.
func TestAssociatedData(t *testing.T) {
	t.Parallel()
	opts := WriteOptions{ClusterID: "cluster-a", Namespace: "default", KeyspaceName: "openbao-local.web", Key: "api-key"}
	for _, tc := range []struct {
		binary bool
		mode   string
	}{{false, "false"}, {true, "true"}} {
		opts.Binary = tc.binary
		got := string(AssociatedData(opts))
		want := Algorithm + "\x00cluster-a\x00default\x00openbao-local.web\x00api-key\x00" + tc.mode
		if got != want {
			t.Fatalf("AssociatedData() = %q, want %q", got, want)
		}
	}
}

// TestEncryptWriteValueCanBeOpenedByRecipient verifies envelope shape and ciphertext contents.
func TestEncryptWriteValueCanBeOpenedByRecipient(t *testing.T) {
	t.Parallel()
	recipientKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	recipient := base64.RawURLEncoding.EncodeToString(recipientKey.PublicKey().Bytes())
	associatedData := []byte("associated-data")
	plaintext := []byte("correct horse battery staple")

	sealed, err := EncryptWriteValue(recipient, plaintext, associatedData)
	if err != nil {
		t.Fatalf("EncryptWriteValue() error = %v", err)
	}
	opened := decryptWriteValueForTest(t, recipientKey, sealed, associatedData)
	if string(opened) != string(plaintext) {
		t.Fatalf("decrypted plaintext = %q, want %q", opened, plaintext)
	}
	if _, err := openWriteEnvelopeForTest(recipientKey, sealed, []byte("wrong-associated-data")); err == nil {
		t.Fatalf("decrypt with wrong associated data succeeded")
	}
}

// TestEncryptWriteValueRejectsInvalidPublicKey verifies recipient public key validation.
func TestEncryptWriteValueRejectsInvalidPublicKey(t *testing.T) {
	t.Parallel()
	if _, err := EncryptWriteValue("not-base64", []byte("value"), nil); err == nil {
		t.Fatalf("EncryptWriteValue() error = nil, want error")
	}
}

// decryptWriteValueForTest opens an encrypted write value and fails the test on errors.
func decryptWriteValueForTest(t *testing.T, recipientKey *ecdh.PrivateKey, sealed string, associatedData []byte) []byte {
	t.Helper()
	plaintext, err := openWriteEnvelopeForTest(recipientKey, sealed, associatedData)
	if err != nil {
		t.Fatalf("openWriteEnvelopeForTest() error = %v", err)
	}
	return plaintext
}

// openWriteEnvelopeForTest decrypts an encrypted write envelope with the recipient private key.
func openWriteEnvelopeForTest(recipientKey *ecdh.PrivateKey, sealed string, associatedData []byte) ([]byte, error) {
	data, ok := strings.CutPrefix(sealed, "podplane-secrets-v1.")
	if !ok {
		return nil, errInvalidEnvelopeForTest("missing prefix")
	}
	envelopeData, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	var envelope encryptedSecretEnvelope
	if err := json.Unmarshal(envelopeData, &envelope); err != nil {
		return nil, err
	}
	if envelope.Version != "podplane-secrets-v1" {
		return nil, errInvalidEnvelopeForTest("wrong version")
	}
	ephemeralBytes, err := base64.RawURLEncoding.DecodeString(envelope.EphemeralPublicKey)
	if err != nil {
		return nil, err
	}
	ephemeralKey, err := ecdh.X25519().NewPublicKey(ephemeralBytes)
	if err != nil {
		return nil, err
	}
	sharedSecret, err := recipientKey.ECDH(ephemeralKey)
	if err != nil {
		return nil, err
	}
	aeadKey, err := hkdf.Key(sha256.New, sharedSecret, ephemeralBytes, "podplane secrets v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aeadKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, associatedData)
}

// errInvalidEnvelopeForTest is a lightweight sentinel error for malformed test envelopes.
type errInvalidEnvelopeForTest string

// Error returns the invalid envelope test error string.
func (e errInvalidEnvelopeForTest) Error() string { return string(e) }
