// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const maxTokenSize = 128 << 10

// IsExpired returns true if the JWT id_token is within `skew` of expiring (or
// already expired). A malformed token is treated as expired.
func IsExpired(idToken string, skew time.Duration) bool {
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	if err := decodeClaims(idToken, &claims); err != nil {
		return true
	}
	expSec, err := strconv.ParseInt(claims.Exp.String(), 10, 64)
	if err != nil {
		return true
	}
	return time.Now().Add(skew).After(time.Unix(expSec, 0))
}

// IdentityFromIDToken extracts the sub claim and the configured username claim
// from an ID token. It does not verify the signature because the issuer already
// verified the token when minting it.
func IdentityFromIDToken(idToken, usernameClaim string) (sub, email string, err error) {
	var raw map[string]any
	if err := decodeClaims(idToken, &raw); err != nil {
		return "", "", err
	}
	if v, ok := raw["sub"].(string); ok {
		sub = v
	}
	if v, ok := raw["email"].(string); ok {
		email = v
	}
	if usernameClaim != "" && usernameClaim != "email" {
		if v, ok := raw[usernameClaim].(string); ok && email == "" {
			email = v
		}
	}
	return sub, email, nil
}

// TrustedIdentity validates the identity constraints Truster must enforce
// for an exchanged workload token.
func TrustedIdentity(idToken, clientID string) (sub, email string, err error) {
	var claims map[string]any
	if err := decodeClaims(idToken, &claims); err != nil {
		return "", "", err
	}
	sub, _ = claims["sub"].(string)
	aud, audOK := claims["aud"].(string)
	_, hasSID := claims["sid"]
	if !strings.HasPrefix(sub, "trusted:") || !audOK || aud != clientID || hasSID {
		return "", "", fmt.Errorf("downstream token does not contain the required trusted identity")
	}
	email, _ = claims["email"].(string)
	return sub, email, nil
}

// validateCompactJWT checks the structural encoding of a compact JWT.
func validateCompactJWT(token string) error {
	if len(token) == 0 || len(token) > maxTokenSize {
		return fmt.Errorf("invalid size")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return fmt.Errorf("invalid compact JWT")
	}
	for i, part := range parts {
		decoded, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return fmt.Errorf("invalid base64url segment")
		}
		if i < 2 {
			var object map[string]any
			if err := json.Unmarshal(decoded, &object); err != nil || object == nil {
				return fmt.Errorf("JWT header and payload must be JSON objects")
			}
		}
	}
	return nil
}

// decodeClaims base64url-decodes the JWT payload (claims segment) of token
// into v.
func decodeClaims(token string, v any) error {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return fmt.Errorf("malformed jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return fmt.Errorf("decode jwt payload: %w", err)
		}
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("parse jwt claims: %w", err)
	}
	return nil
}
