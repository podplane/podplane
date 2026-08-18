// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterauth

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/oidc"
)

// TestServiceLoginStoresAndRenewsToken verifies caching, renewal, CA reuse,
// and subject stability.
func TestServiceLoginStoresAndRenewsToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(config.KeyringPassEnv, "test-password")
	c, err := config.Init()
	if err != nil {
		t.Fatal(err)
	}

	identityFile := filepath.Join(t.TempDir(), "identity.jwt")
	identityToken := serviceJWT("upstream:first", "cluster-client", time.Now().Add(time.Hour))
	if err := os.WriteFile(identityFile, []byte(identityToken), 0600); err != nil {
		t.Fatal(err)
	}
	source := oidc.Source{IdentityFile: identityFile}
	serviceToken := serviceJWT("trusted:github:example", "cluster-client", time.Now().Add(time.Hour))
	nextServiceToken := serviceToken
	var exchanges int
	var exchangedIdentityToken string
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"grant_types_supported":["urn:ietf:params:oauth:grant-type:token-exchange"]}`, server.URL, server.URL+"/authorize", server.URL+"/token")
		case "/token":
			exchanges++
			_ = r.ParseForm()
			exchangedIdentityToken = r.Form.Get("subject_token")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"issued_token_type":"urn:ietf:params:oauth:token-type:id_token","token_type":"Bearer","expires_in":3600}`, nextServiceToken)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "oidc-ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, cert, 0600); err != nil {
		t.Fatal(err)
	}

	cluster := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "cluster", Name: "Cluster", OIDC: clusterconfig.OIDC{IssuerURL: server.URL, ClientID: "cluster-client", CACert: caPath},
	}}
	meta, _, err := ServiceLogin(context.Background(), c, cluster, server.Client(), source, identityToken)
	if err != nil {
		t.Fatal(err)
	}
	if meta.IdentityFile != identityFile || meta.IdentityProvider != "" || meta.OIDCCAPath != caPath {
		t.Fatalf("service metadata = %#v", meta)
	}
	ref := config.AuthRef{Subject: meta.Sub, ClusterID: meta.ClusterID}
	got, err := ResolveToken(c, ref)
	if err != nil || got != serviceToken || exchanges != 1 {
		t.Fatalf("ResolveToken = %q, %v; exchanges = %d", got, err, exchanges)
	}

	expired := serviceJWT(meta.Sub, "cluster-client", time.Now().Add(-time.Minute))
	if err := c.AuthSet(meta, config.AuthSecrets{IDToken: expired}, false); err != nil {
		t.Fatal(err)
	}
	rotatedIdentityToken := serviceJWT("upstream:second", "cluster-client", time.Now().Add(time.Hour))
	if err := os.WriteFile(identityFile, []byte(rotatedIdentityToken), 0600); err != nil {
		t.Fatal(err)
	}
	nextServiceToken = serviceJWT(meta.Sub, "cluster-client", time.Now().Add(time.Hour))
	got, err = ResolveToken(c, ref)
	if err != nil || got != nextServiceToken || exchanges != 2 || exchangedIdentityToken != rotatedIdentityToken {
		t.Fatalf("renewed ResolveToken = %q, %v; exchanges = %d; identity = %q", got, err, exchanges, exchangedIdentityToken)
	}

	if err := c.AuthSet(meta, config.AuthSecrets{IDToken: expired}, false); err != nil {
		t.Fatal(err)
	}
	nextServiceToken = serviceJWT("trusted:different", "cluster-client", time.Now().Add(time.Hour))
	_, err = ResolveToken(c, ref)
	if err == nil || !strings.Contains(err.Error(), "subject does not match") {
		t.Fatalf("mismatched renewal error = %v", err)
	}
	_, secrets, err := c.AuthGet(ref)
	if err != nil || secrets.IDToken != expired {
		t.Fatalf("failed renewal replaced token: %#v, %v", secrets, err)
	}
}

// serviceJWT builds a structurally valid JWT for service-login tests.
func serviceJWT(sub, aud string, expires time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":%q,"aud":%q,"exp":%d}`, sub, aud, expires.Unix())))
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return strings.Join([]string{header, payload, signature}, ".")
}
