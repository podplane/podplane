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
				Expected: 35 * time.Second,
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
				Key:       "secrets-store-csi-driver",
				Name:      "secrets-store-csi-driver",
				Kind:      "daemonsets",
				Required:  true,
				DependsOn: []string{"cilium"},
				Expected:  20 * time.Second,
				Timeout:   3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readSecretsStoreCSI(ctx, opts.KubeContext, opts.Kubeconfig)
				},
			},
			{
				Key:       "podplane-operator",
				Name:      "podplane-operator",
				Kind:      "deployment",
				Required:  true,
				DependsOn: []string{"secrets-store-csi-driver"},
				Expected:  15 * time.Second,
				Timeout:   3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-podplane-operator", "deployment", "platform-podplane-operator")
				},
			},
			{
				Key:       "envoy-gateway",
				Name:      "envoy-gateway",
				Kind:      "deployment",
				Required:  true,
				DependsOn: []string{"podplane-operator"},
				Expected:  20 * time.Second,
				Timeout:   3 * time.Minute,
				Run: func(ctx context.Context) Result {
					return readWorkload(ctx, opts.KubeContext, opts.Kubeconfig, "platform-envoy-gateway", "deployment", "envoy-gateway")
				},
			},
			{
				Key:       "ingress",
				Name:      "local ingress proxy",
				Kind:      "ingress",
				Required:  true,
				DependsOn: []string{"envoy-gateway"},
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

// readSecretsStoreCSI reports ready only when both the CSI driver and the
// OpenBao provider DaemonSets are ready.
func readSecretsStoreCSI(ctx context.Context, kubeContext, kubeconfig string) Result {
	driver := readWorkload(ctx, kubeContext, kubeconfig, "platform-secrets-store-csi-driver", "daemonset", "platform-secrets-store-csi-driver")
	if !driver.Ready {
		return driver
	}
	provider := readWorkload(ctx, kubeContext, kubeconfig, "platform-secrets-store-csi-provider-openbao", "daemonset", "platform-secrets-store-csi-provider-openbao-csi-provider")
	if !provider.Ready {
		return provider
	}
	return Result{Exists: true, Ready: true, Status: StatusReady, Message: "driver and OpenBao provider ready"}
}

// LocalIngressProxyCheck verifies that the local ingress URL is reachable after
// envoy-gateway is expected to be running. Any HTTP response proves the proxy path is
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
		return Result{Exists: true, Status: StatusPending, Message: fmt.Sprintf("waiting for envoy-gateway via %s: %v", url, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout {
		return Result{Exists: true, Status: StatusPending, Message: fmt.Sprintf("waiting for envoy-gateway via %s: HTTP %d", url, resp.StatusCode)}
	}
	return Result{Exists: true, Ready: true, Status: StatusReady, Message: fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode)}
}
