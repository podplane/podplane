// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
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
		return ciliumChecks(opts)
	case seeds.Recommended:
		checks := ciliumChecks(opts)
		checks = append(checks,
			WorkloadCheck(opts.KubeContext, opts.Kubeconfig, "platform-cert-manager", "deployment", "platform-cert-manager-webhook", true),
			CertManagerAdmissionCheck(opts.KubeContext, opts.Kubeconfig, true),
			WorkloadCheck(opts.KubeContext, opts.Kubeconfig, "platform-traefik", "daemonset", "platform-traefik", true),
			LocalIngressProxyCheck(opts.LocalIngressURL, true),
		)
		return checks
	default:
		return nil
	}
}

// ciliumChecks returns the local-start checks required for a functional CNI.
func ciliumChecks(opts LocalStartOptions) []Check {
	return []Check{
		WorkloadCheck(opts.KubeContext, opts.Kubeconfig, "platform-cilium", "daemonset", "cilium", true),
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
			if localIngressURL == nil {
				return Result{Status: StatusPending, Message: "local manager unavailable"}
			}
			url, err := localIngressURL()
			if err != nil {
				return Result{Status: StatusPending, Message: err.Error()}
			}
			client := &http.Client{
				Timeout:   5 * time.Second,
				Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
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
			return Result{Exists: true, Ready: true, Status: StatusReady, Message: fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode)}
		},
	}
}
