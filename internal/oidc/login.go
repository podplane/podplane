// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/browser"
)

// Tokens is the result of a successful auth-code or refresh exchange.
type Tokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Login runs the OIDC authorization-code + PKCE flow and returns the resulting
// tokens. The caller supplies the HTTP client (it is responsible for any
// TLS/CA configuration the issuer needs). callbackPort 0 defaults to 8000.
//
// In interactive mode (headless == false) the user's browser is opened to
// the authorize URL and we wait for the issuer to redirect back to
// http://localhost:<callbackPort>/callback.
//
// In headless mode we GET the authorize URL ourselves with redirects
// disabled and pull the `code` straight out of the Location header. This
// works against any issuer that does not require interactive consent for the
// request — e.g. a confidential client with an existing session, an issuer
// configured to skip the consent screen for trusted clients, or our local
// fake OIDC. It is intentionally not coupled to the local provider.
func Login(ctx context.Context, client *http.Client, issuerURL, clientID string, callbackPort int, headless bool) (*Tokens, error) {
	if issuerURL == "" {
		return nil, fmt.Errorf("login: issuer URL is required")
	}
	if clientID == "" {
		return nil, fmt.Errorf("login: client ID is required")
	}
	if callbackPort == 0 {
		callbackPort = 8000
	}
	disc, err := Discover(ctx, client, issuerURL)
	if err != nil {
		return nil, err
	}

	verifier, challenge, err := newPKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(16)
	if err != nil {
		return nil, err
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", callbackPort)
	authorizeURL := buildAuthorizeURL(disc.AuthorizationEndpoint, authorizeParams{
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		Scope:         "openid profile email offline_access",
		State:         state,
		CodeChallenge: challenge,
	})

	var code string
	if headless {
		code, err = headlessAuthorize(ctx, client, authorizeURL, state)
	} else {
		code, err = browserAuthorize(ctx, authorizeURL, callbackPort, state)
	}
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", verifier)
	return postToken(ctx, client, disc.TokenEndpoint, form)
}

// Refresh exchanges refreshToken for a fresh id_token (and possibly a new
// refresh_token) against the configured issuer.
func Refresh(ctx context.Context, client *http.Client, issuerURL, clientID, refreshToken string) (*Tokens, error) {
	if issuerURL == "" {
		return nil, fmt.Errorf("refresh: issuer URL is required")
	}
	if clientID == "" {
		return nil, fmt.Errorf("refresh: client ID is required")
	}
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh: refreshToken is required")
	}
	disc, err := Discover(ctx, client, issuerURL)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	return postToken(ctx, client, disc.TokenEndpoint, form)
}

// authorizeParams groups the query parameters added to the authorize URL.
type authorizeParams struct {
	ClientID      string
	RedirectURI   string
	Scope         string
	State         string
	CodeChallenge string
}

func buildAuthorizeURL(endpoint string, p authorizeParams) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	if strings.Contains(endpoint, "?") {
		return endpoint + "&" + q.Encode()
	}
	return endpoint + "?" + q.Encode()
}

// browserAuthorize opens the user's browser to authorizeURL and starts a
// localhost HTTP server on `port`, waiting for /callback to receive the
// authorization code.
func browserAuthorize(ctx context.Context, authorizeURL string, port int, expectedState string) (string, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return "", fmt.Errorf("listen on callback port %d: %w", port, err)
	}

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code, err := readCallback(r, expectedState)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			resCh <- result{err: err}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Logged in</h1><p>You can close this tab and return to the terminal.</p></body></html>"))
		resCh <- result{code: code}
	})

	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Opening browser to %s\n", authorizeURL)
	if err := browser.OpenURL(authorizeURL); err != nil {
		fmt.Printf("Failed to open browser automatically; please open the URL manually:\n  %s\n", authorizeURL)
	}

	select {
	case r := <-resCh:
		return r.code, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// headlessAuthorize GETs the authorize URL with redirects disabled and pulls
// the `code` out of the Location header.
func headlessAuthorize(ctx context.Context, client *http.Client, authorizeURL, expectedState string) (string, error) {
	// Clone so we don't mutate the caller's redirect policy.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
	if err != nil {
		return "", fmt.Errorf("build authorize request: %w", err)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", fmt.Errorf("authorize: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("authorize: expected redirect, got HTTP %d: %s", resp.StatusCode, string(body))
	}
	loc, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("authorize: missing Location header: %w", err)
	}
	r := &http.Request{URL: loc}
	return readCallback(r, expectedState)
}

// readCallback validates the OIDC callback's query string and returns the
// `code`.
func readCallback(r *http.Request, expectedState string) (string, error) {
	q := r.URL.Query()
	if errStr := q.Get("error"); errStr != "" {
		return "", fmt.Errorf("authorize error: %s: %s", errStr, q.Get("error_description"))
	}
	if got := q.Get("state"); got != expectedState {
		return "", fmt.Errorf("authorize state mismatch")
	}
	code := q.Get("code")
	if code == "" {
		return "", fmt.Errorf("authorize callback missing code")
	}
	return code, nil
}

func postToken(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint %s: HTTP %d: %s", endpoint, resp.StatusCode, string(body))
	}
	t := &Tokens{}
	if err := json.Unmarshal(body, t); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if t.IDToken == "" {
		return nil, fmt.Errorf("token endpoint returned no id_token: %s", string(body))
	}
	return t, nil
}

// newPKCE returns a (verifier, challenge) pair using S256.
func newPKCE() (verifier, challenge string, err error) {
	verifier, err = randomURLSafe(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
