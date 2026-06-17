// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package fakevault

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const kubernetesServiceAccountIssuer = "https://localhost"

// KubernetesTokenValidator validates Kubernetes service-account JWTs against
// the kube-apiserver JWKS endpoint for a cluster.
type KubernetesTokenValidator struct {
	KubernetesAPIURL func(clusterID string) (string, error)
	Client           *http.Client
	Issuer           string
}

// ValidateToken validates a Vault/OpenBao Kubernetes auth login JWT.
func (v *KubernetesTokenValidator) ValidateToken(ctx context.Context, clusterID, _, rawToken string) error {
	if v.KubernetesAPIURL == nil {
		return fmt.Errorf("kubernetes api url resolver is required")
	}
	apiURL, err := v.KubernetesAPIURL(clusterID)
	if err != nil {
		return err
	}
	jwksURL := strings.TrimRight(apiURL, "/") + "/openid/v1/jwks"
	keySet, err := v.fetchJWKSet(ctx, jwksURL, rawToken)
	if err != nil {
		return fmt.Errorf("fetch kubernetes jwks: %w", err)
	}
	tok, err := jwt.Parse(
		[]byte(rawToken),
		jwt.WithKeySet(keySet, jws.WithInferAlgorithmFromKey(true)),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer()),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("verify kubernetes service-account jwt: %w", err)
	}
	if !strings.HasPrefix(tok.Subject(), "system:serviceaccount:") {
		return fmt.Errorf("jwt subject is not a kubernetes service account")
	}
	return nil
}

// fetchJWKSet fetches and parses a kube-apiserver JWKS endpoint using the
// submitted service-account token as the bearer token. Local VM kube-apiserver runs
// with anonymous auth disabled, so the JWKS endpoint is not public.
func (v *KubernetesTokenValidator) fetchJWKSet(ctx context.Context, jwksURL, rawToken string) (jwk.Set, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build jwks request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)
	resp, err := v.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jwks endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	set, err := jwk.ParseReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse jwks: %w", err)
	}
	return set, nil
}

// httpClient returns the HTTP client used to fetch the kube-apiserver JWKS
// endpoint.
func (v *KubernetesTokenValidator) httpClient() *http.Client {
	if v.Client != nil {
		return v.Client
	}
	return http.DefaultClient
}

// issuer returns the accepted Kubernetes service-account token issuer.
func (v *KubernetesTokenValidator) issuer() string {
	if v.Issuer != "" {
		return v.Issuer
	}
	return kubernetesServiceAccountIssuer
}
