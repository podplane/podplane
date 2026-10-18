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

const (
	operatorRole                    = "podplane-operator"
	operatorServiceAccountNamespace = "platform-podplane-operator"
	operatorServiceAccountName      = "platform-podplane-operator"
)

// KubernetesTokenValidator validates Kubernetes service-account JWTs against
// the kube-apiserver JWKS endpoint for a cluster.
type KubernetesTokenValidator struct {
	KubernetesAPIURL func(clusterID string) (string, error)
	KubernetesIssuer func(clusterID string) (string, error)
	Client           *http.Client
	Issuer           string
}

// ValidateIdentity verifies a Vault/OpenBao Kubernetes auth login JWT and requested
// role, then returns the authenticated service-account identity and JWT expiry.
func (v *KubernetesTokenValidator) ValidateIdentity(ctx context.Context, clusterID, role, rawToken string) (Identity, error) {
	if v.KubernetesAPIURL == nil {
		return Identity{}, fmt.Errorf("kubernetes api url resolver is required")
	}
	apiURL, err := v.KubernetesAPIURL(clusterID)
	if err != nil {
		return Identity{}, err
	}
	issuer := v.Issuer
	if v.KubernetesIssuer != nil {
		issuer, err = v.KubernetesIssuer(clusterID)
		if err != nil {
			return Identity{}, fmt.Errorf("resolve kubernetes service-account issuer: %w", err)
		}
	}
	if issuer == "" {
		return Identity{}, fmt.Errorf("kubernetes service-account issuer is required")
	}
	jwksURL := strings.TrimRight(apiURL, "/") + "/openid/v1/jwks"
	keySet, err := v.fetchJWKSet(ctx, jwksURL, rawToken)
	if err != nil {
		return Identity{}, fmt.Errorf("fetch kubernetes jwks: %w", err)
	}
	tok, err := jwt.Parse(
		[]byte(rawToken),
		jwt.WithKeySet(keySet, jws.WithInferAlgorithmFromKey(true)),
		jwt.WithValidate(true),
		jwt.WithIssuer(issuer),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	if err != nil {
		return Identity{}, fmt.Errorf("verify kubernetes service-account jwt: %w", err)
	}
	serviceAccountNamespace, serviceAccountName, ok := serviceAccountSubject(tok.Subject())
	if !ok {
		return Identity{}, fmt.Errorf("jwt subject is not a kubernetes service account")
	}
	if !tok.Expiration().After(time.Now()) {
		return Identity{}, fmt.Errorf("jwt expiration is required and must be in the future")
	}
	if role == operatorRole {
		if serviceAccountNamespace != operatorServiceAccountNamespace || serviceAccountName != operatorServiceAccountName {
			return Identity{}, fmt.Errorf("operator role %q is only valid for service account %s/%s", role, operatorServiceAccountNamespace, operatorServiceAccountName)
		}
	} else if role != serviceAccountName {
		return Identity{}, fmt.Errorf("role %q does not match service account %q", role, serviceAccountName)
	}
	return Identity{Namespace: serviceAccountNamespace, ServiceAccount: serviceAccountName, ExpiresAt: tok.Expiration()}, nil
}

// serviceAccountSubject parses a Kubernetes service-account JWT subject.
func serviceAccountSubject(subject string) (namespace, name string, ok bool) {
	parts := strings.Split(subject, ":")
	if len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" || parts[2] == "" || parts[3] == "" {
		return "", "", false
	}
	return parts[2], parts[3], true
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
	defer func() { _ = resp.Body.Close() }()
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
