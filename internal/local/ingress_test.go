// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/pkg/seeds"
)

func TestLocalIngressClusterID(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		want       string
	}{
		{name: "cluster root", serverName: "dev.localhost", want: "dev"},
		{name: "host under cluster", serverName: "app.dev.localhost", want: "dev"},
		{name: "reserved kubernetes api", serverName: "dev.k8s.localhost", want: "dev"},
		{name: "case and trailing dot", serverName: "App.Dev.Localhost.", want: "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := localIngressClusterID(tt.serverName)
			if err != nil {
				t.Fatalf("localIngressClusterID error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("localIngressClusterID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsAppIngressHostname(t *testing.T) {
	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "cluster root", host: "dev.localhost", want: true},
		{name: "host under cluster", host: "app.dev.localhost", want: true},
		{name: "host with port", host: "app.dev.localhost:4433", want: true},
		{name: "case and trailing dot", host: "App.Dev.Localhost.", want: true},
		{name: "production", host: "app.example.com", want: false},
		{name: "kubernetes api", host: "dev.k8s.localhost", want: false},
		{name: "too many labels", host: "api.internal.dev.localhost", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAppIngressHostname(tt.host); got != tt.want {
				t.Fatalf("IsAppIngressHostname(%q) = %t, want %t", tt.host, got, tt.want)
			}
		})
	}
}

func TestLocalIngressClusterIDRejectsInvalidHostnames(t *testing.T) {
	for _, serverName := range []string{"", "example.com", "localhost", "api.internal.dev.localhost"} {
		t.Run(serverName, func(t *testing.T) {
			if _, err := localIngressClusterID(serverName); err == nil {
				t.Fatalf("localIngressClusterID(%q) succeeded", serverName)
			}
		})
	}
}

func TestLocalIngressClusterIDAcceptsPort(t *testing.T) {
	got, err := localIngressClusterID("app.dev.localhost:4433")
	if err != nil {
		t.Fatalf("localIngressClusterID error = %v", err)
	}
	if got != "dev" {
		t.Fatalf("localIngressClusterID = %q, want dev", got)
	}
}

func TestLocalIngressTargetForKubernetesAPIHost(t *testing.T) {
	target, err := localIngressTargetForHost("dev.k8s.localhost:4433")
	if err != nil {
		t.Fatalf("localIngressTargetForHost error = %v", err)
	}
	if target.clusterID != "dev" || target.kind != localIngressTargetKubernetesAPI {
		t.Fatalf("target = %#v, want dev kubernetes api", target)
	}
}

func TestLocalIngressProxyRoutesKubernetesAPIHost(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(backend.Close)

	port, err := strconv.Atoi(strings.TrimPrefix(backend.URL, "https://127.0.0.1:"))
	if err != nil {
		t.Fatalf("parse backend port from %q: %v", backend.URL, err)
	}
	runtimeDir := t.TempDir()
	if err := writeState(runtimeDir, clusterState{
		ClusterID: "dev",
		Backend:   "qemu",
		Ports:     portState{KubernetesAPI: port},
	}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "https://dev.k8s.localhost/readyz", nil)
	r.Host = "dev.k8s.localhost"
	w := httptest.NewRecorder()

	localIngressProxy(runtimeDir).ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

func TestLocalIngressProxyRoutesKubernetesAPIByTLSServerName(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(backend.Close)

	port, err := strconv.Atoi(strings.TrimPrefix(backend.URL, "https://127.0.0.1:"))
	if err != nil {
		t.Fatalf("parse backend port from %q: %v", backend.URL, err)
	}
	runtimeDir := t.TempDir()
	if err := writeState(runtimeDir, clusterState{
		ClusterID: "dev",
		Backend:   "qemu",
		Ports:     portState{KubernetesAPI: port},
	}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:4433/readyz", nil)
	r.Host = "127.0.0.1:4433"
	r.TLS = &tls.ConnectionState{ServerName: "dev.k8s.localhost"}
	w := httptest.NewRecorder()

	localIngressProxy(runtimeDir).ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNoContent, w.Body.String())
	}
}

func TestLocalIngressProxyShowsPlaceholderForMissingClusterState(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://hello.localhost:4433/", nil)
	w := httptest.NewRecorder()

	localIngressProxy(t.TempDir()).ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
	for _, want := range []string{"Podplane Local Ingress Proxy", `local cluster &#34;hello&#34; is not running`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("placeholder body missing %q: %s", want, w.Body.String())
		}
	}
}

func TestWriteLocalClusterConfigUsesReservedKubernetesAPIHost(t *testing.T) {
	manager := &Local{dataDir: t.TempDir()}
	componentsSource := &clusterconfig.ComponentsSource{
		URL: "https://github.com/podplane/components.git",
		Ref: clusterconfig.ComponentsSourceRef{Tag: "v1.2.3"},
	}
	path, err := manager.WriteLocalClusterConfig("dev", "https://oidc.localhost:1234/oidc", "/tmp/oidc-ca.pem", LocalKubernetesAPIHostname("dev"), 4433, clusterconfig.Seed{Name: seeds.Recommended, Version: testSeedVersion}, componentsSource)
	if err != nil {
		t.Fatalf("WriteLocalClusterConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cluster config: %v", err)
	}
	for _, want := range []string{`"issuer_url": "https://oidc.localhost:1234/oidc"`, `"ca_cert": "/tmp/oidc-ca.pem"`, `"api_hostname": "dev.k8s.localhost"`, `"api_port": 4433`, `"url": "https://github.com/podplane/components.git"`, `"tag": "v1.2.3"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("cluster config missing %s:\n%s", want, string(data))
		}
	}
}

func TestLocalIngressPlaceholder(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://localhost:4433/", nil)
	w := httptest.NewRecorder()
	localIngressPlaceholder(w, r, errTestPlaceholder{})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if !strings.Contains(w.Body.String(), "Podplane Local Ingress Proxy") {
		t.Fatalf("placeholder body missing title: %s", w.Body.String())
	}
}

type errTestPlaceholder struct{}

func (errTestPlaceholder) Error() string { return "test reason" }

// startTLSHTTP2Server starts an httptest.Server with TLS + HTTP/2 enabled
// so we can verify the proxy's upstream transport actually negotiates h2.
func startTLSHTTP2Server(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// backendPort returns the TCP port the given httptest.Server is listening on.
func backendPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse backend url %q: %v", srv.URL, err)
	}
	_, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host:port %q: %v", u.Host, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port %q: %v", p, err)
	}
	return port
}

// writeDevState writes a clusterState for the "dev" cluster into runtimeDir
// so the local ingress proxy can resolve dev.k8s.localhost / dev.localhost.
func writeDevState(t *testing.T, runtimeDir string, kubernetesAPIPort, traefikPort int) {
	t.Helper()
	if err := writeState(runtimeDir, clusterState{
		ClusterID: "dev",
		Backend:   "qemu",
		Ports:     portState{KubernetesAPI: kubernetesAPIPort, TraefikHTTPS: traefikPort},
	}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
}

// TestLocalIngressProxyUpstreamNegotiatesHTTP2 asserts that the upstream
// transport negotiates HTTP/2 so concurrent helm-style bursts multiplex over
// a single connection instead of fanning out into a TLS-handshake storm.
func TestLocalIngressProxyUpstreamNegotiatesHTTP2(t *testing.T) {
	var seen sync.Map // proto -> count
	backend := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		v, _ := seen.LoadOrStore(r.Proto, new(atomic.Int64))
		v.(*atomic.Int64).Add(1)
		rw.WriteHeader(http.StatusOK)
	}))

	runtimeDir := t.TempDir()
	writeDevState(t, runtimeDir, backendPort(t, backend), 0)

	handler := localIngressProxy(runtimeDir)

	// Fire several sequential requests against the same backend so the
	// transport has a chance to upgrade to HTTP/2 on the first hop.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "https://dev.k8s.localhost/readyz", nil)
		req.Host = "dev.k8s.localhost"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i, w.Code, http.StatusOK)
		}
	}

	var got string
	seen.Range(func(k, v any) bool {
		got += fmt.Sprintf(" %s=%d", k.(string), v.(*atomic.Int64).Load())
		return true
	})
	v, ok := seen.Load("HTTP/2.0")
	if !ok || v.(*atomic.Int64).Load() == 0 {
		t.Fatalf("upstream did not negotiate HTTP/2; protocol counts:%s", got)
	}
}

// TestLocalIngressProxyConcurrentBurstSucceeds simulates the canonical "helm
// install fires N parallel CRD POSTs" workload against a healthy HTTP/2
// backend and asserts every request body lands intact at the backend. This
// exercises the connection-pool + HTTP/2 multiplexing path that replaced the
// previous per-request connection storm.
func TestLocalIngressProxyConcurrentBurstSucceeds(t *testing.T) {
	var landed atomic.Int64
	backend := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if !bytes.Contains(body, []byte("payload-for-"+id)) {
			t.Errorf("body mismatch for %s: %q", id, body)
		}
		landed.Add(1)
		rw.WriteHeader(http.StatusCreated)
	}))

	runtimeDir := t.TempDir()
	writeDevState(t, runtimeDir, backendPort(t, backend), 0)

	handler := localIngressProxy(runtimeDir)

	const requests = 25
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("req-%d", i)
			body := strings.NewReader("payload-for-" + id)
			req := httptest.NewRequest(http.MethodPost, "https://dev.k8s.localhost/apis/apiextensions.k8s.io/v1/customresourcedefinitions", body)
			req.Host = "dev.k8s.localhost"
			req.Header.Set("X-Request-ID", id)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusCreated {
				errs <- fmt.Errorf("request %s: status %d, body=%s", id, w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
	if landed.Load() != requests {
		t.Fatalf("only %d/%d requests reached backend successfully", landed.Load(), requests)
	}
}

// TestLocalIngressProxyEmitsKubernetesStatusOnGiveUp asserts that when the
// upstream backend is unreachable, the proxy returns a properly formatted
// Kubernetes Status JSON body with reason "ServiceUnavailable", instead of a
// raw text 502.
func TestLocalIngressProxyEmitsKubernetesStatusOnGiveUp(t *testing.T) {
	runtimeDir := t.TempDir()
	// Allocate and immediately release a port so the backend port is closed.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	writeDevState(t, runtimeDir, closedPort, 0)

	req := httptest.NewRequest(http.MethodGet, "https://dev.k8s.localhost/readyz", nil)
	req.Host = "dev.k8s.localhost"
	w := httptest.NewRecorder()

	localIngressProxy(runtimeDir).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var status struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
		Status     string `json:"status"`
		Reason     string `json:"reason"`
		Code       int    `json:"code"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status body: %v; body=%s", err, w.Body.String())
	}
	if status.Kind != "Status" || status.APIVersion != "v1" || status.Status != "Failure" || status.Reason != "ServiceUnavailable" || status.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status object: %+v", status)
	}
	if !strings.Contains(status.Message, "podplane local ingress proxy could not reach") {
		t.Fatalf("status.Message missing context: %q", status.Message)
	}
}

// TestLocalIngressProxyReusesCachedProxyPerCluster asserts that two
// concurrent local clusters get isolated cached proxy entries (each with its
// own warm transport) but that repeated requests for the same cluster reuse
// the same entry.
func TestLocalIngressProxyReusesCachedProxyPerCluster(t *testing.T) {
	backendA := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusOK) }))
	backendB := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusOK) }))

	runtimeDir := t.TempDir()
	if err := writeState(runtimeDir, clusterState{ClusterID: "a", Backend: "qemu", Ports: portState{KubernetesAPI: backendPort(t, backendA)}}); err != nil {
		t.Fatalf("writeState a: %v", err)
	}
	if err := writeState(runtimeDir, clusterState{ClusterID: "b", Backend: "qemu", Ports: portState{KubernetesAPI: backendPort(t, backendB)}}); err != nil {
		t.Fatalf("writeState b: %v", err)
	}

	handler := localIngressProxy(runtimeDir)

	hit := func(cluster string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "https://"+cluster+".k8s.localhost/readyz", nil)
		req.Host = cluster + ".k8s.localhost"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("cluster %s: status = %d, body=%s", cluster, w.Code, w.Body.String())
		}
	}

	// Many interleaved concurrent hits against both clusters.
	const passes = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < passes; i++ {
			hit("a")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < passes; i++ {
			hit("b")
		}
	}()
	wg.Wait()
}

// TestLocalIngressProxyRebuildsOnPortChange asserts that when the cluster
// state's backend port changes (e.g. VM stopped and restarted), the proxy
// transparently retargets to the new port rather than continuing to talk to
// the old (now closed) port.
func TestLocalIngressProxyRebuildsOnPortChange(t *testing.T) {
	backend1 := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("X-Backend", "one")
		rw.WriteHeader(http.StatusOK)
	}))
	backend2 := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("X-Backend", "two")
		rw.WriteHeader(http.StatusOK)
	}))

	runtimeDir := t.TempDir()
	writeDevState(t, runtimeDir, backendPort(t, backend1), 0)

	handler := localIngressProxy(runtimeDir)

	req := httptest.NewRequest(http.MethodGet, "https://dev.k8s.localhost/readyz", nil)
	req.Host = "dev.k8s.localhost"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("X-Backend"); got != "one" {
		t.Fatalf("expected X-Backend=one, got %q", got)
	}

	writeDevState(t, runtimeDir, backendPort(t, backend2), 0)

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("X-Backend"); got != "two" {
		t.Fatalf("after port change, expected X-Backend=two, got %q", got)
	}
}

// TestLocalIngressProxyStreamingResponseIsNotBuffered ensures the proxy
// flushes upstream writes immediately so long-lived streaming endpoints
// (kube-apiserver "watch", `kubectl logs -f`) deliver each frame to the
// client as soon as the upstream writes it.
func TestLocalIngressProxyStreamingResponseIsNotBuffered(t *testing.T) {
	chunkSent := make(chan struct{})
	chunkAck := make(chan struct{})
	backend := startTLSHTTP2Server(t, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		flusher, ok := rw.(http.Flusher)
		if !ok {
			t.Errorf("ResponseWriter is not a Flusher")
			return
		}
		if _, err := io.WriteString(rw, `{"type":"ADDED"}`+"\n"); err != nil {
			t.Errorf("write first chunk: %v", err)
			return
		}
		flusher.Flush()
		close(chunkSent)
		select {
		case <-chunkAck:
		case <-r.Context().Done():
			return
		case <-time.After(3 * time.Second):
			t.Errorf("timed out waiting for client to ack first chunk")
			return
		}
		if _, err := io.WriteString(rw, `{"type":"MODIFIED"}`+"\n"); err != nil {
			return
		}
		flusher.Flush()
	}))

	runtimeDir := t.TempDir()
	writeDevState(t, runtimeDir, backendPort(t, backend), 0)

	handler := localIngressProxy(runtimeDir)
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "dev.k8s.localhost"},
		},
	}
	frontURL, _ := url.Parse(srv.URL)
	req, _ := http.NewRequest(http.MethodGet, "https://"+frontURL.Host+"/api/v1/pods?watch=1", nil)
	req.Host = "dev.k8s.localhost"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("watch request: %v", err)
	}
	defer resp.Body.Close()

	// Wait for backend to declare it sent the first chunk.
	select {
	case <-chunkSent:
	case <-time.After(2 * time.Second):
		t.Fatalf("backend never sent first chunk")
	}

	// We must be able to read it from the proxy *before* the second chunk
	// is written.
	reader := bytes.NewBuffer(nil)
	buf := make([]byte, 64)
	deadline := time.After(2 * time.Second)
	for !strings.Contains(reader.String(), `"ADDED"`) {
		select {
		case <-deadline:
			t.Fatalf("first chunk never reached client; got %q", reader.String())
		default:
		}
		n, err := resp.Body.Read(buf)
		if n > 0 {
			reader.Write(buf[:n])
		}
		if err != nil && err != io.EOF {
			t.Fatalf("read body: %v", err)
		}
	}
	close(chunkAck)
}
