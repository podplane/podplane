// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const testKubernetesServiceAccountIssuer = "https://cluster.example.com"

// TestKubernetesTokenValidatorAcceptsServiceAccountJWT verifies JWKS-backed
// Kubernetes service-account token validation.
func TestKubernetesTokenValidatorAcceptsServiceAccountJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	set, err := testJWKS(key)
	if err != nil {
		t.Fatalf("build jwks: %v", err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openid/v1/jwks" {
			http.NotFound(rw, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(rw, "missing bearer token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(rw).Encode(set)
	}))
	t.Cleanup(server.Close)

	rawToken, err := testServiceAccountJWT(key)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	validator := &KubernetesTokenValidator{
		KubernetesAPIURL: func(string) (string, error) { return server.URL, nil },
		KubernetesIssuer: func(string) (string, error) { return testKubernetesServiceAccountIssuer, nil },
		Client:           server.Client(),
	}
	if err := validator.ValidateToken(context.Background(), "dev", "default", rawToken); err != nil {
		t.Fatalf("validate token: %v", err)
	}
}

// TestKubernetesTokenValidatorAcceptsOperatorRole verifies local fakevault
// accepts the operator role only for the operator service account.
func TestKubernetesTokenValidatorAcceptsOperatorRole(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	validator := testKubernetesTokenValidator(t, key)
	rawToken, err := testServiceAccountJWTForSubject(key, "system:serviceaccount:platform-podplane-operator:platform-podplane-operator")
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	if err := validator.ValidateToken(context.Background(), "dev", "podplane-operator", rawToken); err != nil {
		t.Fatalf("validate token: %v", err)
	}
}

// TestKubernetesTokenValidatorRejectsMismatchedRole verifies workload roles
// must match the service account name and cannot claim the operator role.
func TestKubernetesTokenValidatorRejectsMismatchedRole(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	validator := testKubernetesTokenValidator(t, key)
	rawToken, err := testServiceAccountJWTForSubject(key, "system:serviceaccount:default:hello")
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	if err := validator.ValidateToken(context.Background(), "dev", "hello", rawToken); err != nil {
		t.Fatalf("validate matching workload role: %v", err)
	}
	if err := validator.ValidateToken(context.Background(), "dev", "other", rawToken); err == nil {
		t.Fatalf("expected mismatched workload role error")
	}
	if err := validator.ValidateToken(context.Background(), "dev", "podplane-operator", rawToken); err == nil {
		t.Fatalf("expected operator role error for non-operator service account")
	}
}

func testKubernetesTokenValidator(t *testing.T, key *rsa.PrivateKey) *KubernetesTokenValidator {
	t.Helper()
	set, err := testJWKS(key)
	if err != nil {
		t.Fatalf("build jwks: %v", err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openid/v1/jwks" {
			http.NotFound(rw, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(rw, "missing bearer token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(rw).Encode(set)
	}))
	t.Cleanup(server.Close)
	return &KubernetesTokenValidator{
		KubernetesAPIURL: func(string) (string, error) { return server.URL, nil },
		Issuer:           testKubernetesServiceAccountIssuer,
		Client:           server.Client(),
	}
}

// testJWKS returns a single-key JWKS for signing local validator tests.
func testJWKS(key *rsa.PrivateKey) (jwk.Set, error) {
	pub, err := jwk.FromRaw(key.Public())
	if err != nil {
		return nil, fmt.Errorf("build public jwk: %w", err)
	}
	if err := pub.Set(jwk.KeyIDKey, "test"); err != nil {
		return nil, err
	}
	if err := pub.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		return nil, err
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		return nil, err
	}
	return set, nil
}

// testServiceAccountJWT signs a Kubernetes service-account-shaped JWT for
// local validator tests.
func testServiceAccountJWT(key *rsa.PrivateKey) (string, error) {
	return testServiceAccountJWTForSubject(key, "system:serviceaccount:default:default")
}

func testServiceAccountJWTForSubject(key *rsa.PrivateKey, subject string) (string, error) {
	now := time.Now()
	tok, err := jwt.NewBuilder().
		Issuer(testKubernetesServiceAccountIssuer).
		Subject(subject).
		IssuedAt(now).
		Expiration(now.Add(time.Hour)).
		Build()
	if err != nil {
		return "", err
	}
	headers := jws.NewHeaders()
	_ = headers.Set(jws.KeyIDKey, "test")
	signed, err := jwt.NewSerializer().
		Sign(jwt.WithKey(jwa.RS256, key, jws.WithProtectedHeaders(headers))).
		Serialize(tok)
	if err != nil {
		return "", err
	}
	return string(signed), nil
}
