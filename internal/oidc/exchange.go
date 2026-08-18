// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

const tokenExchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"

// Exchange performs RFC 8693 token exchange and interprets Truster's
// access_token as the downstream Kubernetes ID token.
func Exchange(ctx context.Context, client *http.Client, issuerURL, clientID, subjectToken string) (*Tokens, error) {
	disc, err := Discover(ctx, client, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}
	if len(disc.GrantTypesSupported) > 0 && !slices.Contains(disc.GrantTypesSupported, tokenExchangeGrant) {
		return nil, fmt.Errorf("OIDC server does not support service login (RFC 8693 token exchange)")
	}
	form := url.Values{
		"grant_type":           {tokenExchangeGrant},
		"subject_token":        {subjectToken},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:id_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:id_token"},
		"client_id":            {clientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, disc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token exchange request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var oauthError struct {
			Error string `json:"error"`
		}
		if decodeResponse(resp.Body, &oauthError) == nil && oauthError.Error == "unsupported_grant_type" {
			return nil, fmt.Errorf("OIDC server does not support service login (RFC 8693 token exchange)")
		}
		return nil, fmt.Errorf("token exchange failed with HTTP %d", resp.StatusCode)
	}
	var out struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
		TokenType       string `json:"token_type"`
		IDToken         string `json:"id_token"`
		RefreshToken    string `json:"refresh_token"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := decodeResponse(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("token exchange response was invalid")
	}
	const idType = "urn:ietf:params:oauth:token-type:id_token"
	if out.AccessToken == "" || out.IssuedTokenType != idType || !strings.EqualFold(out.TokenType, "Bearer") || out.ExpiresIn <= 0 || out.IDToken != "" || out.RefreshToken != "" {
		return nil, fmt.Errorf("token exchange response did not satisfy the Truster ID-token contract")
	}
	if err := validateCompactJWT(out.AccessToken); err != nil {
		return nil, fmt.Errorf("token exchange returned an invalid downstream token")
	}
	return &Tokens{IDToken: out.AccessToken, TokenType: out.TokenType, ExpiresIn: out.ExpiresIn}, nil
}
