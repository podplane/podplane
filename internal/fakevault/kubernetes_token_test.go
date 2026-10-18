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
	if _, err := validator.ValidateIdentity(context.Background(), "dev", "default", rawToken); err != nil {
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
	if _, err := validator.ValidateIdentity(context.Background(), "dev", "podplane-operator", rawToken); err != nil {
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
	for _, tt := range []struct {
		name    string
		subject string
		role    string
	}{
		{"other workload role", "system:serviceaccount:default:hello", "other"},
		{"operator role", "system:serviceaccount:default:hello", "podplane-operator"},
		{"operator name in wrong namespace", "system:serviceaccount:default:platform-podplane-operator", "podplane-operator"},
		{"wrong name in operator namespace", "system:serviceaccount:platform-podplane-operator:hello", "podplane-operator"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rawToken, err := testServiceAccountJWTForSubject(key, tt.subject)
			if err != nil {
				t.Fatalf("sign jwt: %v", err)
			}
			if _, err := validator.ValidateIdentity(context.Background(), "dev", tt.role, rawToken); err == nil {
				t.Fatal("accepted mismatched role")
			}
		})
	}
}

// TestKubernetesTokenValidatorRejectsInvalidJWT checks signature, issuer, and expiry validation.
func TestKubernetesTokenValidatorRejectsInvalidJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	validator := testKubernetesTokenValidator(t, key)
	for _, tt := range []struct {
		name             string
		issuer           string
		ttl              time.Duration
		invalidSignature bool
	}{
		{"signature", testKubernetesServiceAccountIssuer, time.Hour, true},
		{"issuer", "https://wrong.example", time.Hour, false},
		{"expired", testKubernetesServiceAccountIssuer, -time.Second, false},
		{"missing expiry", testKubernetesServiceAccountIssuer, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			builder := jwt.NewBuilder().Issuer(tt.issuer).Subject("system:serviceaccount:team-a:api")
			if tt.ttl != 0 {
				builder = builder.Expiration(time.Now().Add(tt.ttl))
			}
			token, err := builder.Build()
			if err != nil {
				t.Fatal(err)
			}
			headers := jws.NewHeaders()
			if err := headers.Set(jws.KeyIDKey, "test"); err != nil {
				t.Fatal(err)
			}
			signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256, key, jws.WithProtectedHeaders(headers)))
			if err != nil {
				t.Fatal(err)
			}
			rawToken := string(signed)
			if tt.invalidSignature {
				parts := strings.Split(rawToken, ".")
				rawToken = parts[0] + "." + parts[1] + ".AAAA"
			}
			if _, err := validator.ValidateIdentity(context.Background(), "dev", "api", rawToken); err == nil {
				t.Fatal("accepted invalid JWT")
			}
		})
	}
}

// testKubernetesTokenValidator creates a validator backed by an authenticated test JWKS server.
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

// testServiceAccountJWTForSubject signs an hour-long service account JWT with the supplied subject.
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
