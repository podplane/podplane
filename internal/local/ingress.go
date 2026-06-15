// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	localIngressHTTPSPort      = 4433
	localTraefikHTTPSHostname  = "127.0.0.1"
	localKubernetesAPIHostname = "127.0.0.1"
)

// Options for the local ingress proxy upstream transport. These are
// intentionally generous: this proxy runs on the developer's machine and only
// talks to local QEMU hostfwd ports for the local cluster VMs, so connection
// pool growth is bounded by the number of local clusters and there is no
// remote network cost to keeping connections warm.
const (
	proxyMaxIdleConns          = 256
	proxyMaxIdleConnsPerHost   = 128
	proxyMaxConnsPerHost       = 0 // unbounded; HTTP/2 multiplexes
	proxyIdleConnTimeout       = 90 * time.Second
	proxyTLSHandshakeTimeout   = 10 * time.Second
	proxyExpectContinueTimeout = 1 * time.Second
	proxyDialTimeout           = 5 * time.Second
	proxyKeepAlive             = 30 * time.Second
	proxyResponseHeaderTimeout = 0 // unbounded: kube-apiserver "watch" requests are long-lived
)

type localIngressTargetKind string

const (
	localIngressTargetTraefik       localIngressTargetKind = "traefik"
	localIngressTargetKubernetesAPI localIngressTargetKind = "kubernetes-api"
)

type localIngressTarget struct {
	clusterID string
	kind      localIngressTargetKind
}

// LocalKubernetesAPIHostname returns the reserved host routed by the local
// ingress proxy to a cluster's Kubernetes API server.
func LocalKubernetesAPIHostname(clusterID string) string {
	return fmt.Sprintf("%s.k8s.localhost", clusterID)
}

// IsAppIngressHostname reports whether host is in the local app ingress
// namespace. App ingress uses <cluster-id>.localhost or
// <host>.<cluster-id>.localhost; <cluster-id>.k8s.localhost is reserved for the
// Kubernetes API.
func IsAppIngressHostname(host string) bool {
	target, err := localIngressTargetForHost(host)
	return err == nil && target.kind == localIngressTargetTraefik
}

// LocalIngressClusterID extracts the local cluster ID from a browser-facing
// local app ingress hostname.
func LocalIngressClusterID(host string) (string, error) {
	target, err := localIngressTargetForHost(host)
	if err != nil {
		return "", err
	}
	if target.kind != localIngressTargetTraefik {
		return "", fmt.Errorf("local ingress hostname %q is reserved for Kubernetes API routing", host)
	}
	return target.clusterID, nil
}

// AppIngressRoutePort returns the browser-facing local ingress HTTPS port when
// host belongs to clusterID's app ingress namespace and the local server is
// running.
func AppIngressRoutePort(runtimeDir, host, clusterID string) int {
	hostClusterID, err := LocalIngressClusterID(host)
	if err != nil || hostClusterID != clusterID {
		return 0
	}
	pidFile, err := ServerPIDFile(runtimeDir)
	if err != nil {
		return 0
	}
	if running, err := pidFile.IsRunning(); err != nil || !running {
		return 0
	}
	port, _ := strconv.Atoi(pidFile.GetData("ingress_https_port"))
	return port
}

// localIngressTargetForHost extracts the local cluster and target from an
// ingress hostname. App ingress uses <cluster-id>.localhost or
// <host>.<cluster-id>.localhost. The reserved <cluster-id>.k8s.localhost host
// routes to the Kubernetes API server and is intentionally outside the app
// ingress namespace.
func localIngressTargetForHost(host string) (localIngressTarget, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return localIngressTarget{}, fmt.Errorf("local ingress TLS requires SNI")
	}
	if strings.HasSuffix(host, ".k8s.localhost") {
		clusterID := strings.TrimSuffix(host, ".k8s.localhost")
		if clusterID == "" || strings.Contains(clusterID, ".") {
			return localIngressTarget{}, fmt.Errorf("local Kubernetes API hostname %q must be <cluster-id>.k8s.localhost", host)
		}
		return localIngressTarget{clusterID: clusterID, kind: localIngressTargetKubernetesAPI}, nil
	}
	if !strings.HasSuffix(host, ".localhost") {
		return localIngressTarget{}, fmt.Errorf("local ingress TLS hostname %q is not under .localhost", host)
	}
	name := strings.TrimSuffix(host, ".localhost")
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return localIngressTarget{}, fmt.Errorf("local ingress TLS hostname %q has too many labels before .localhost", host)
	}
	clusterID := parts[len(parts)-1]
	if clusterID == "" {
		return localIngressTarget{}, fmt.Errorf("local ingress TLS hostname %q does not include a cluster ID", host)
	}
	if clusterID == "k8s" {
		return localIngressTarget{}, fmt.Errorf("local ingress TLS hostname %q is reserved for Kubernetes API routing", host)
	}
	return localIngressTarget{clusterID: clusterID, kind: localIngressTargetTraefik}, nil
}

// localIngressClusterID extracts the local cluster ID from any valid local
// ingress hostname. It accepts either SNI-style hostnames or HTTP Host header
// values with ports.
func localIngressClusterID(host string) (string, error) {
	target, err := localIngressTargetForHost(host)
	if err != nil {
		return "", err
	}
	return target.clusterID, nil
}

// cachedProxy holds a *httputil.ReverseProxy along with the upstream target
// (host:port string) it was built for. If the upstream target changes (e.g.,
// the cluster VM was stopped and restarted with a different host port), the
// entry is rebuilt and the old one is discarded; the discarded entry's idle
// connections close naturally on IdleConnTimeout.
type cachedProxy struct {
	target string
	proxy  *httputil.ReverseProxy
}

// localIngressProxy builds the local TLS ingress reverse proxy to either the
// VM's raw Traefik HTTPS endpoint or the reserved Kubernetes API endpoint.
//
// The returned handler maintains a process-lifetime cache of one warm
// *httputil.ReverseProxy per (clusterID, ingress kind) so that:
//   - bursts of concurrent requests to the same backend share a connection
//     pool instead of repeatedly paying TLS handshake + slow-start costs;
//   - HTTP/2 is negotiated to the backend, multiplexing burst requests over a
//     small number of TCP connections (critical for QEMU SLiRP usermode
//     networking, which struggles with parallel connection storms);
//   - long-lived streaming endpoints (kube-apiserver watches, log streaming,
//     `kubectl exec`/`kubectl port-forward` upgrades) are flushed
//     frame-by-frame rather than buffered.
func localIngressProxy(runtimeDir string) http.Handler {
	var (
		mu    sync.RWMutex
		cache = map[localIngressTarget]*cachedProxy{}
	)

	// resolve returns the cached *cachedProxy for the given ingress target
	// and upstream host:port, building (or rebuilding, if the target host
	// has changed since the entry was created) the underlying
	// *httputil.ReverseProxy on first use. Concurrent callers requesting
	// the same key race on a sync.RWMutex; the double-checked pattern under
	// the write lock keeps that race correct.
	resolve := func(key localIngressTarget, target string, targetName string) *cachedProxy {
		mu.RLock()
		entry, ok := cache[key]
		mu.RUnlock()
		if ok && entry.target == target {
			return entry
		}
		mu.Lock()
		defer mu.Unlock()
		if entry, ok := cache[key]; ok && entry.target == target {
			return entry
		}
		u := &url.URL{Scheme: "https", Host: target}
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.Transport = newUpstreamTransport()
		// Flush each upstream write immediately so streaming endpoints
		// (kube-apiserver watches, `kubectl logs -f`) don't get buffered
		// inside the proxy.
		proxy.FlushInterval = -1
		proxy.ErrorHandler = func(rw http.ResponseWriter, r *http.Request, err error) {
			// Traefik gets a friendly text 502 so a developer hitting an
			// app URL before Traefik is installed sees an actionable
			// message rather than a Kubernetes-shaped error body.
			if key.kind == localIngressTargetTraefik {
				http.Error(rw, fmt.Sprintf("local ingress proxy to %s is unavailable: %v; ensure Traefik is installed and running in the local cluster", targetName, err), http.StatusBadGateway)
				return
			}
			writeKubernetesAPIProxyError(rw, r, err)
		}
		entry = &cachedProxy{target: target, proxy: proxy}
		cache[key] = entry
		return entry
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		ingressTarget, err := localIngressTargetForHost(r.Host)
		if err != nil && r.TLS != nil && r.TLS.ServerName != "" {
			ingressTarget, err = localIngressTargetForHost(r.TLS.ServerName)
		}
		if err != nil {
			localIngressPlaceholder(rw, r, err)
			return
		}
		state, err := readState(runtimeDir, ingressTarget.clusterID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				localIngressPlaceholder(rw, r, fmt.Errorf("local cluster %q is not running", ingressTarget.clusterID))
				return
			}
			http.Error(rw, fmt.Sprintf("local ingress proxy failed to read cluster state: %v", err), http.StatusBadGateway)
			return
		}
		port, targetHost, targetName := state.Ports.TraefikHTTPS, localTraefikHTTPSHostname, "VM Traefik"
		if ingressTarget.kind == localIngressTargetKubernetesAPI {
			port, targetHost, targetName = state.Ports.KubernetesAPI, localKubernetesAPIHostname, "VM Kubernetes API"
		}
		if port == 0 {
			http.Error(rw, fmt.Sprintf("local ingress proxy failed to resolve %s port: state is missing port", targetName), http.StatusBadGateway)
			return
		}
		resolve(ingressTarget, net.JoinHostPort(targetHost, strconv.Itoa(port)), targetName).proxy.ServeHTTP(rw, r)
	})
}

// newUpstreamTransport returns an *http.Transport tuned for proxying many
// concurrent requests to the local cluster VM Kubernetes API or Traefik.
//
// HTTP/2 is explicitly enabled via ForceAttemptHTTP2 because Go's net/http
// package conservatively disables auto-upgrade to HTTP/2 whenever a custom
// TLSClientConfig is supplied. Negotiating HTTP/2 to the upstream is the key
// robustness lever for this proxy: it lets bursts of requests (e.g., helm's
// parallel CRD applies during a cold cluster install) multiplex over one
// connection instead of triggering a TLS-handshake / connection storm.
func newUpstreamTransport() *http.Transport {
	return &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   proxyDialTimeout,
			KeepAlive: proxyKeepAlive,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			//nolint:gosec // upstream is loopback QEMU hostfwd to VM TLS endpoints:
			// kube-apiserver uses fake Nstance CA; Traefik may use in-cluster/self-signed local certs.
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2", "http/1.1"},
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          proxyMaxIdleConns,
		MaxIdleConnsPerHost:   proxyMaxIdleConnsPerHost,
		MaxConnsPerHost:       proxyMaxConnsPerHost,
		IdleConnTimeout:       proxyIdleConnTimeout,
		TLSHandshakeTimeout:   proxyTLSHandshakeTimeout,
		ExpectContinueTimeout: proxyExpectContinueTimeout,
		ResponseHeaderTimeout: proxyResponseHeaderTimeout,
		DisableCompression:    false,
	}
}

// writeKubernetesAPIProxyError writes a Kubernetes API Status object body
// describing a failed upstream proxy request. client-go consumers (kubectl,
// helm, controllers) parse this as a metav1.Status and surface a clean
// "ServiceUnavailable" error instead of an opaque text gateway message.
func writeKubernetesAPIProxyError(rw http.ResponseWriter, r *http.Request, err error) {
	status := struct {
		Kind       string         `json:"kind"`
		APIVersion string         `json:"apiVersion"`
		Metadata   map[string]any `json:"metadata"`
		Status     string         `json:"status"`
		Message    string         `json:"message"`
		Reason     string         `json:"reason"`
		Code       int            `json:"code"`
	}{
		Kind:       "Status",
		APIVersion: "v1",
		Metadata:   map[string]any{},
		Status:     "Failure",
		Message:    fmt.Sprintf("podplane local ingress proxy could not reach the local cluster VM Kubernetes API (%s %s): %v", r.Method, r.URL.Path, err),
		Reason:     "ServiceUnavailable",
		Code:       http.StatusServiceUnavailable,
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(rw).Encode(&status)
}

//go:embed ingress.html
var localIngressPlaceholderHTML string

// localIngressPlaceholder writes a static response for requests that reach the
// local ingress server without a valid local cluster ingress hostname.
func localIngressPlaceholder(rw http.ResponseWriter, _ *http.Request, reason error) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(rw, localIngressPlaceholderHTML, localIngressHTTPSPort, html.EscapeString(reason.Error()))
}
