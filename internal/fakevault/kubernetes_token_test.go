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
		Client:           server.Client(),
	}
	if err := validator.ValidateToken(context.Background(), "dev", "default", rawToken); err != nil {
		t.Fatalf("validate token: %v", err)
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
	now := time.Now()
	tok, err := jwt.NewBuilder().
		Issuer(kubernetesServiceAccountIssuer).
		Subject("system:serviceaccount:default:default").
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
