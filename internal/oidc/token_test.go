// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import "testing"

// TestIdentityFromIDTokenUsesConfiguredClaim verifies username extraction.
func TestIdentityFromIDTokenUsesConfiguredClaim(t *testing.T) {
	token := testJWT("trusted:github:acme:deploy")
	sub, email, err := IdentityFromIDToken(token, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if sub != "trusted:github:acme:deploy" || email != sub {
		t.Fatalf("IdentityFromIDToken = %q, %q; want trusted subject", sub, email)
	}
}
