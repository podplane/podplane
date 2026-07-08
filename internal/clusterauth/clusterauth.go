// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/kubectl"
	"github.com/podplane/podplane/internal/local"
	"github.com/podplane/podplane/internal/oidc"
	"github.com/podplane/podplane/internal/oidcserver"
)

// Options configures a single Login or Refresh call. The
// caller supplies an HTTP client already configured to trust the issuer's CA
// (clusterauth doesn't do TLS plumbing).
type Options struct {
	Cluster      *clusterconfig.ClusterConfig
	HTTPClient   *http.Client
	CallbackPort int
	Headless     bool
	Local        bool
}

// Login performs the OIDC login flow against opts.Cluster and persists the
// resulting tokens (metadata to the config file, secrets to the OS keyring).
// It does not touch kubectl; command handlers configure kubectl separately.
func Login(ctx context.Context, c *config.Config, opts Options) (config.AuthMetadata, oidc.Tokens, error) {
	issuerURL := ""
	clientID := ""
	if opts.Cluster != nil {
		issuerURL = opts.Cluster.Cluster.OIDC.IssuerURL
		clientID = opts.Cluster.ResolvedClientID()
	}
	tokens, err := oidc.Login(ctx, opts.HTTPClient, issuerURL, clientID, opts.CallbackPort, opts.Headless)
	if err != nil {
		return config.AuthMetadata{}, oidc.Tokens{}, fmt.Errorf("login: %w", err)
	}
	meta, err := persistTokens(c, opts.Cluster, tokens, opts.Local)
	if err != nil {
		return config.AuthMetadata{}, oidc.Tokens{}, err
	}
	return meta, *tokens, nil
}

// Refresh exchanges refreshToken for fresh tokens against opts.Cluster's
// issuer, persists the result against meta, and returns the new tokens. Used
// by the kubectl-auth hook before falling back to a full Login.
func Refresh(ctx context.Context, c *config.Config, opts Options, meta config.AuthMetadata, refreshToken string) (oidc.Tokens, error) {
	issuerURL := ""
	clientID := ""
	if opts.Cluster != nil {
		issuerURL = opts.Cluster.Cluster.OIDC.IssuerURL
		clientID = opts.Cluster.ResolvedClientID()
	}
	tokens, err := oidc.Refresh(ctx, opts.HTTPClient, issuerURL, clientID, refreshToken)
	if err != nil {
		return oidc.Tokens{}, err
	}
	newRefresh := tokens.RefreshToken
	if newRefresh == "" {
		newRefresh = refreshToken
	}
	if err := c.AuthSet(meta, config.AuthSecrets{IDToken: tokens.IDToken, RefreshToken: newRefresh}, opts.Local); err != nil {
		return oidc.Tokens{}, fmt.Errorf("save refreshed tokens: %w", err)
	}
	return *tokens, nil
}

// ResolveToken implements the kubectl auth hook token waterfall: cached token,
// refresh token, then fresh login.
func ResolveToken(c *config.Config, clusterID, sub string) (string, error) {
	isLocal := localClusterConfigExists(c, clusterID)
	meta, secrets, err := c.AuthGet(sub, clusterID, isLocal)
	if err != nil {
		return "", fmt.Errorf("read auth state: %w", err)
	}

	if secrets.IDToken != "" && !oidc.IsExpired(secrets.IDToken, 60*time.Second) {
		return secrets.IDToken, nil
	}

	cluster, isLocal, err := loadClusterForHook(c, clusterID, meta)
	if err != nil {
		return "", err
	}
	httpClient, err := NewOIDCHTTPClient(c, cluster)
	if err != nil {
		return "", err
	}

	opts := Options{Cluster: cluster, HTTPClient: httpClient, Local: isLocal}
	if secrets.RefreshToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tokens, err := Refresh(ctx, c, opts, meta, secrets.RefreshToken)
		if err == nil {
			return tokens.IDToken, nil
		}
	}

	opts.Headless = isLocal
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, tokens, err := Login(ctx, c, opts)
	if err != nil {
		return "", err
	}
	return tokens.IDToken, nil
}

// Logout clears cached auth state and matching kubectl access for a cluster.
func Logout(c *config.Config, stdout io.Writer, clusterID string, local bool) error {
	entries, err := c.AuthListByCluster(clusterID, local)
	if err != nil {
		return err
	}
	subs := make([]string, 0, len(entries)+1)
	if local {
		subs = append(subs, oidcserver.LocalSub)
	}
	if len(entries) == 0 && !local {
		_, _ = fmt.Fprintf(stdout, "No cached credentials for cluster %q\n", clusterID)
	}
	for _, e := range entries {
		subs = append(subs, e.Sub)
		if err := c.AuthDelete(e.Sub, e.ClusterID, local); err != nil {
			return fmt.Errorf("delete auth for %s: %w", e.Sub, err)
		}
		user := e.UserEmail
		if user == "" {
			user = e.Sub
		}
		_, _ = fmt.Fprintf(stdout, "Cleared credentials for %s on cluster %s\n", user, e.ClusterID)
	}
	if local {
		if err := c.AuthDelete(oidcserver.LocalSub, clusterID, true); err != nil {
			return fmt.Errorf("delete local auth for %s: %w", oidcserver.LocalSub, err)
		}
	}
	if err := kubectl.DeleteClusterAccess(stdout, clusterID, subs, local); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Cleared kubectl access for cluster %q\n", clusterID)
	return nil
}

// LogoutLocal clears cached auth state and kubectl access for a local cluster.
func LogoutLocal(stdout io.Writer, clusterID string) error {
	c, restoreKeyringPass, err := config.InitWithLocalKeyring()
	if err != nil {
		return err
	}
	defer restoreKeyringPass()
	return Logout(c, stdout, clusterID, true)
}

// localClusterConfigExists reports whether a generated local cluster config exists.
func localClusterConfigExists(c *config.Config, clusterID string) bool {
	_, err := os.Stat(local.ClusterConfigPath(c.DataDirectory(), clusterID))
	return err == nil
}

func loadClusterForHook(c *config.Config, clusterID string, meta config.AuthMetadata) (*clusterconfig.ClusterConfig, bool, error) {
	localConfigPath := local.ClusterConfigPath(c.DataDirectory(), clusterID)
	if _, err := os.Stat(localConfigPath); err == nil {
		cfg, err := clusterconfig.Load(localConfigPath)
		return cfg, true, err
	}
	if meta.Issuer == "" || meta.ClientID == "" {
		return nil, false, fmt.Errorf("no cluster config found for %s and stored metadata is incomplete; run `podplane login -f <cluster.jsonc>`", clusterID)
	}
	return &clusterconfig.ClusterConfig{
		Cluster: clusterconfig.Cluster{
			ID: meta.ClusterID,
			OIDC: clusterconfig.OIDC{
				IssuerURL: meta.Issuer,
				ClientID:  meta.ClientID,
			},
		},
	}, false, nil
}

// NewOIDCHTTPClient returns an *http.Client suitable for talking to
// cluster's OIDC issuer. It resolves cluster.Cluster.OIDC.CACert (inline
// PEM / URL / file path) via c.ResolveCACert("oidc-ca", spec) and trusts
// that CA in addition to the system roots. Callers that want a different
// transport (e.g. tests with a mock) can build their own *http.Client and
// pass it directly to Login or Refresh via Options.HTTPClient.
func NewOIDCHTTPClient(c *config.Config, cluster *clusterconfig.ClusterConfig) (*http.Client, error) {
	caCertPath, err := c.ResolveCACert("oidc-ca", cluster.Cluster.OIDC.CACert)
	if err != nil {
		return nil, fmt.Errorf("resolve oidc ca cert: %w", err)
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport is %T, want *http.Transport", http.DefaultTransport)
	}
	transport := tr.Clone()
	if caCertPath != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("read ca cert %s: %w", caCertPath, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca cert %s contained no usable certificates", caCertPath)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	issuerURL, issuerErr := url.Parse(cluster.Cluster.OIDC.IssuerURL)
	if issuerErr == nil && strings.EqualFold(issuerURL.Hostname(), "oidc.localhost") {
		manager := local.NewManager(c, cluster.Cluster.ID)
		localHTTPSPort, err := manager.LocalServerHTTPSPort()
		if err != nil {
			return nil, err
		}
		// Local clusters use a fake OIDC server run by the Podplane CLI at oidc.localhost
		// Some host resolvers do not treat subdomains of localhost as loopback.
		// Dial the local fake OIDC issuer on 127.0.0.1 while preserving the
		// original URL host for HTTP Host and TLS server-name verification.
		dialer := &net.Dialer{}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err == nil && strings.EqualFold(host, "oidc.localhost") {
				if localHTTPSPort != "" {
					port = localHTTPSPort
				}
				address = net.JoinHostPort("127.0.0.1", port)
			}
			return dialer.DialContext(ctx, network, address)
		}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}, nil
}

// persistTokens parses identity from tokens.IDToken, builds the matching
// AuthMetadata for cluster, and writes both to the config + keyring.
func persistTokens(c *config.Config, cluster *clusterconfig.ClusterConfig, tokens *oidc.Tokens, local bool) (config.AuthMetadata, error) {
	sub, email, err := oidc.IdentityFromIDToken(tokens.IDToken, cluster.ResolvedUsernameClaim())
	if err != nil {
		return config.AuthMetadata{}, fmt.Errorf("inspect id_token: %w", err)
	}
	if sub == "" {
		return config.AuthMetadata{}, fmt.Errorf("id_token has no `sub` claim")
	}
	meta := config.AuthMetadata{
		Sub:         sub,
		ClusterID:   cluster.Cluster.ID,
		ClusterName: cluster.Cluster.Name,
		Issuer:      cluster.Cluster.OIDC.IssuerURL,
		ClientID:    cluster.ResolvedClientID(),
		UserEmail:   email,
	}
	if err := c.AuthSet(meta, config.AuthSecrets{
		IDToken:      tokens.IDToken,
		RefreshToken: tokens.RefreshToken,
	}, local); err != nil {
		return config.AuthMetadata{}, err
	}
	return meta, nil
}
