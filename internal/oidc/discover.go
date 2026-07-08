// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Discovery is the subset of an OIDC discovery document that Podplane uses.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Discover fetches and parses the issuer's /.well-known/openid-configuration
// document using the supplied client.
func Discover(ctx context.Context, client *http.Client, issuerURL string) (*Discovery, error) {
	url := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("discovery %s: HTTP %d: %s", url, resp.StatusCode, string(body))
	}
	d := &Discovery{}
	if err := json.NewDecoder(resp.Body).Decode(d); err != nil {
		return nil, fmt.Errorf("parse discovery: %w", err)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" {
		return nil, fmt.Errorf("discovery %s: missing authorization_endpoint or token_endpoint", url)
	}
	return d, nil
}
