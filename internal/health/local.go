// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/podplane/podplane/pkg/seeds"
)

// LocalStartOptions configures post-API health checks for podplane local start.
type LocalStartOptions struct {
	SeedName        string
	KubeContext     string
	Kubeconfig      string
	LocalIngressURL func() (string, error)
}

// LocalStartChecks returns seed-specific local-start post-API health checks.
func LocalStartChecks(opts LocalStartOptions) []Check {
	switch opts.SeedName {
	case seeds.Minimal:
		return []Check{
			{
				Key:      "cilium",
				Name:     "cilium",
				Kind:     "daemonset",
				Required: true,
				Expected: 30 * time.Second,
				Timeout:  3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-cilium", "daemonset", "cilium")
				},
			},
		}
	case seeds.Recommended:
		return []Check{
			{
				Key:      "cilium",
				Name:     "cilium",
				Kind:     "daemonset",
				Required: true,
				Expected: 30 * time.Second,
				Timeout:  3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-cilium", "daemonset", "cilium")
				},
			},
			{
				Key:       "cert-manager",
				Name:      "cert-manager webhook",
				Kind:      "deployment",
				Required:  true,
				DependsOn: []string{"cilium"},
				Expected:  30 * time.Second,
				Timeout:   3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-cert-manager", "deployment", "platform-cert-manager-webhook")
				},
			},
			{
				Key:       "cert-manager-admission",
				Name:      "cert-manager admission",
				Kind:      "webhook",
				Required:  true,
				DependsOn: []string{"cert-manager"},
				Expected:  10 * time.Second,
				Timeout:   2 * time.Minute,
				Run: func(ctx context.Context) Result {
					return checkCertManagerAdmission(ctx, opts.KubeContext, opts.Kubeconfig)
				},
			},
			{
				Key:       "trust-manager",
				Name:      "trust-manager",
				Kind:      "deployment",
				Required:  true,
				DependsOn: []string{"cert-manager-admission"},
				Expected:  30 * time.Second,
				Timeout:   3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-trust-manager", "deployment", "platform-trust-manager")
				},
			},
			{
				Key:       "default-app-trust-bundle",
				Name:      "default app trust bundle",
				Kind:      "configmap",
				Required:  true,
				DependsOn: []string{"trust-manager"},
				Expected:  10 * time.Second,
				Timeout:   2 * time.Minute,
				Run: func(ctx context.Context) Result {
					return checkConfigMapData(ctx, opts.KubeContext, opts.Kubeconfig, "default", "platform-selfsigned-ca-bundle", "ca.crt")
				},
			},
			{
				Key:       "traefik",
				Name:      "traefik",
				Kind:      "daemonset",
				Required:  true,
				DependsOn: []string{"cilium"},
				Expected:  20 * time.Second,
				Timeout:   3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-traefik", "daemonset", "platform-traefik")
				},
			},
			{
				Key:       "ingress",
				Name:      "local ingress proxy",
				Kind:      "ingress",
				Required:  true,
				DependsOn: []string{"traefik"},
				Expected:  5 * time.Second,
				Timeout:   time.Minute,
				Run: func(ctx context.Context) Result {
					return checkLocalIngressProxy(ctx, opts.LocalIngressURL)
				},
			},
		}
	default:
		return nil
	}
}

// LocalIngressProxyCheck verifies that the local ingress URL is reachable after
// Traefik is expected to be running. Any HTTP response proves the proxy path is
// accepting browser traffic; connection failures keep the check pending.
func LocalIngressProxyCheck(localIngressURL func() (string, error), required bool) Check {
	return Check{
		Key:      "local/ingress/proxy",
		Name:     "local ingress proxy",
		Kind:     "ingress",
		Required: required,
		Run: func(ctx context.Context) Result {
			return checkLocalIngressProxy(ctx, localIngressURL)
		},
	}
}

// checkLocalIngressProxy verifies that the local ingress URL responds to an HTTP
// request.
func checkLocalIngressProxy(ctx context.Context, localIngressURL func() (string, error)) Result {
	if localIngressURL == nil {
		return Result{Status: StatusPending, Message: "local manager unavailable"}
	}
	url, err := localIngressURL()
	if err != nil {
		return Result{Status: StatusPending, Message: err.Error()}
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err == nil && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
					addr = net.JoinHostPort("127.0.0.1", port)
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{Err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Exists: true, Status: StatusPending, Message: fmt.Sprintf("waiting for Traefik via %s: %v", url, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
		return Result{Exists: true, Status: StatusPending, Message: fmt.Sprintf("waiting for Traefik via %s: HTTP %d", url, resp.StatusCode)}
	}
	return Result{Exists: true, Ready: true, Status: StatusReady, Message: fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode)}
}
