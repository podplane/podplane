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

// ValidateToken validates a Vault/OpenBao Kubernetes auth login JWT.
func (v *KubernetesTokenValidator) ValidateToken(ctx context.Context, clusterID, role, rawToken string) error {
	if v.KubernetesAPIURL == nil {
		return fmt.Errorf("kubernetes api url resolver is required")
	}
	apiURL, err := v.KubernetesAPIURL(clusterID)
	if err != nil {
		return err
	}
	issuer := v.Issuer
	if v.KubernetesIssuer != nil {
		issuer, err = v.KubernetesIssuer(clusterID)
		if err != nil {
			return fmt.Errorf("resolve kubernetes service-account issuer: %w", err)
		}
	}
	if issuer == "" {
		return fmt.Errorf("kubernetes service-account issuer is required")
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
		jwt.WithIssuer(issuer),
		jwt.WithAcceptableSkew(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("verify kubernetes service-account jwt: %w", err)
	}
	serviceAccountNamespace, serviceAccountName, ok := serviceAccountSubject(tok.Subject())
	if !ok {
		return fmt.Errorf("jwt subject is not a kubernetes service account")
	}
	if role == operatorRole {
		if serviceAccountNamespace != operatorServiceAccountNamespace || serviceAccountName != operatorServiceAccountName {
			return fmt.Errorf("operator role %q is only valid for service account %s/%s", role, operatorServiceAccountNamespace, operatorServiceAccountName)
		}
		return nil
	}
	if role != serviceAccountName {
		return fmt.Errorf("role %q does not match service account %q", role, serviceAccountName)
	}
	return nil
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
