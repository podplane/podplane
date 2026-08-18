// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	ProviderGitHub    = "github"
	ProviderBuildkite = "buildkite"
)

// SourceOptions controls explicit source selection. Environ and LookPath are
// test hooks; nil values use the process environment and exec.LookPath.
type SourceOptions struct {
	IdentityProvider string
	IdentityFile     string
	Environ          func(string) string
	LookPath         func(string) (string, error)
}

// Source is non-secret metadata describing an upstream token source.
type Source struct {
	IdentityProvider string
	IdentityFile     string
}

// IsUserLogin reports whether the source requires interactive user login.
func (s Source) IsUserLogin() bool {
	return s.IdentityProvider == "" && s.IdentityFile == ""
}

// SelectSource applies explicit controls and conservative CI auto-detection.
func SelectSource(opts SourceOptions) (Source, error) {
	if opts.IdentityProvider != "" && opts.IdentityFile != "" {
		return Source{}, fmt.Errorf("--identity-provider and --identity-file are mutually exclusive")
	}
	if opts.IdentityFile != "" {
		path, err := filepath.Abs(opts.IdentityFile)
		if err != nil {
			return Source{}, fmt.Errorf("resolve identity file: %w", err)
		}
		return Source{IdentityFile: path}, nil
	}
	if opts.IdentityProvider != "" {
		switch opts.IdentityProvider {
		case "none":
			return Source{}, nil
		case ProviderGitHub, ProviderBuildkite:
			return Source{IdentityProvider: opts.IdentityProvider}, nil
		default:
			return Source{}, fmt.Errorf("unsupported --identity-provider %q (use github, buildkite, or none)", opts.IdentityProvider)
		}
	}
	getenv := opts.Environ
	if getenv == nil {
		getenv = os.Getenv
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	github := getenv("GITHUB_ACTIONS") == "true" && getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != "" && getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") != ""
	_, buildkiteErr := lookPath("buildkite-agent")
	buildkite := getenv("BUILDKITE") == "true" && buildkiteErr == nil
	if github && buildkite {
		return Source{}, fmt.Errorf("both GitHub Actions and Buildkite were detected; select one with --identity-provider, or use --identity-provider=none for a user login")
	}
	if github {
		return Source{IdentityProvider: ProviderGitHub}, nil
	}
	if buildkite {
		return Source{IdentityProvider: ProviderBuildkite}, nil
	}
	return Source{}, nil
}

// CommandContext is replaceable by tests that exercise Buildkite acquisition.
var CommandContext = exec.CommandContext

// Acquire obtains a fresh identity token. It deliberately returns redacted
// errors and reopens identity files on every call.
func Acquire(ctx context.Context, client *http.Client, source Source, clientID string) (string, error) {
	if source.IdentityProvider != "" && source.IdentityFile != "" {
		return "", fmt.Errorf("identity provider and identity file are mutually exclusive")
	}
	var token string
	switch {
	case source.IdentityProvider == ProviderGitHub:
		u, err := url.Parse(os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"))
		if err != nil {
			return "", fmt.Errorf("GitHub Actions OIDC request URL is invalid")
		}
		q := u.Query()
		q.Set("audience", clientID)
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return "", fmt.Errorf("build GitHub Actions OIDC request")
		}
		req.Header.Set("Authorization", "Bearer "+os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("GitHub Actions OIDC request failed")
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("GitHub Actions OIDC request failed with HTTP %d", resp.StatusCode)
		}
		var result struct {
			Value string `json:"value"`
		}
		if err := decodeResponse(resp.Body, &result); err != nil || result.Value == "" {
			return "", fmt.Errorf("GitHub Actions OIDC response was invalid")
		}
		token = result.Value
	case source.IdentityProvider == ProviderBuildkite:
		cmd := CommandContext(ctx, "buildkite-agent", "oidc", "request-token", "--audience", clientID, "--claim", "organization_id,pipeline_id")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("buildkite OIDC token request failed")
		}
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("buildkite OIDC token request failed")
		}
		out, readErr := io.ReadAll(io.LimitReader(stdout, maxTokenSize+1))
		waitErr := cmd.Wait()
		err = readErr
		if err == nil {
			err = waitErr
		}
		if err != nil {
			return "", fmt.Errorf("buildkite OIDC token request failed")
		}
		if len(out) > maxTokenSize {
			return "", fmt.Errorf("buildkite OIDC token was too large")
		}
		token = strings.TrimSpace(string(out))
	case source.IdentityFile != "":
		f, err := os.Open(source.IdentityFile)
		if err != nil {
			return "", fmt.Errorf("open OIDC token file: %w", err)
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return "", fmt.Errorf("inspect OIDC token file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("OIDC token file must be a regular file")
		}
		if info.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("OIDC token file permissions must be owner-only (for example 0600)")
		}
		b, err := io.ReadAll(io.LimitReader(f, maxTokenSize+1))
		if err != nil || len(b) > maxTokenSize {
			return "", fmt.Errorf("OIDC token file could not be read or was too large")
		}
		token = strings.TrimSpace(string(b))
	default:
		if source.IdentityProvider != "" {
			return "", fmt.Errorf("unsupported identity provider %q", source.IdentityProvider)
		}
		return "", fmt.Errorf("identity provider or identity file is required")
	}
	if err := validateCompactJWT(token); err != nil {
		return "", fmt.Errorf("upstream OIDC token is not a valid compact JWT")
	}
	return token, nil
}
