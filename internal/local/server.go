// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/nstance-dev/nstance/pkg/fakeserver"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/fakevault"
	"github.com/podplane/podplane/internal/oidcserver"
	"github.com/podplane/podplane/internal/pid"
	"github.com/podplane/podplane/internal/s3fake"
)

const (
	localGuestHostAddr     = "10.0.2.2"
	localServerLogFilename = "local-server.log"
)

// ServerLogPath returns the local server stdout/stderr log path.
func ServerLogPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, localServerLogFilename)
}

// ServerEnsure ensures the local server is running by running
// `podplane local server` in the background and waiting for it to start.
// Note that this command already handles detection of duplicate PIDs and will
// not start a new process if one is already running (and instead exit
// (immediately). Here we just run the command in the background and expect the
// user will stop the server via the `podplane local stop` or
// `podplane local server -q` commands.
func (m *Local) ServerEnsure(output io.Writer) error {
	if output == nil {
		output = os.Stdout
	}
	// Check if the server is already running before spawning a new process
	pidFile, err := ServerPIDFile(m.runtimeDir)
	if err == nil {
		if isRunning, runErr := pidFile.IsRunning(); runErr == nil && isRunning {
			// Server already running — just save the PID file and return
			m.webserverPIDFile = pidFile
			return nil
		}
	}
	if err := CheckServerRuntimeDependencies(); err != nil {
		return fmt.Errorf("local server runtime dependency check failed: %w", err)
	}

	// Get the path to the current binary
	cmdBin, err := os.Executable()
	if err != nil {
		return err
	}
	// Start the local server in the background.
	fmt.Fprintln(output, "Starting local server...")
	logPath := ServerLogPath(m.runtimeDir)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("failed to create local server log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open local server log file %s: %w", logPath, err)
	}
	defer logFile.Close()
	cmd := execwrap.Command(cmdBin, "local", "server", "--background", "true")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("local server failed to start: %w\nLog: %s", err, logPath)
	}
	if cmd.Process == nil {
		return fmt.Errorf("local server process failed to start\nLog: %s", logPath)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	var newPidFile pid.PIDFile
	started := false
	// Poll until the background server writes a running PID file, while also
	// noticing early child-process exits instead of waiting for the full timeout.
	for range 10 {
		select {
		case err := <-waitCh:
			if err != nil {
				return fmt.Errorf("local server failed to start: %w\nLog: %s", err, logPath)
			}
			return fmt.Errorf("local server failed to start\nLog: %s", logPath)
		default:
		}
		newPidFile, err = ServerPIDFile(m.runtimeDir)
		if err != nil {
			return fmt.Errorf("failed to load local server PID file: %w\nLog: %s", err, logPath)
		}
		if isRunning, err := newPidFile.IsRunning(); err == nil && isRunning {
			started = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !started {
		select {
		case err := <-waitCh:
			if err != nil {
				return fmt.Errorf("local server failed to start: %w\nLog: %s", err, logPath)
			}
			return fmt.Errorf("local server failed to start\nLog: %s", logPath)
		default:
			_ = cmd.Process.Kill()
			<-waitCh
			return fmt.Errorf("local server failed to start\nLog: %s", logPath)
		}
	}
	fmt.Fprintf(output, "- HTTP port: %s\n", newPidFile.GetData("http_port"))
	fmt.Fprintf(output, "- HTTPS port: %s\n", newPidFile.GetData("https_port"))
	fmt.Fprintf(output, "- Log: %s\n", logPath)
	_, _ = color.New(color.FgGreen).Fprintln(output, "✓ Local server started successfully")
	// Save the PID file into the Local struct and return
	m.webserverPIDFile = newPidFile
	return nil
}

// ServerKill stops the local server if it is running and removes the PID file.
func ServerKill(pidFile pid.PIDFile) error {
	// exit early if pid is zero
	if pidFile.PID() == 0 {
		return nil
	}
	// print pid
	fmt.Printf("Stopping local HTTP(S) server on ports %s and %s...\n", pidFile.GetData("http_port"), pidFile.GetData("https_port"))
	// close the server if it is running and remove the pid file
	err := pidFile.Kill()
	if err != nil {
		return fmt.Errorf("Failed to stop local server: %w", err)
	}
	return nil
}

// ServerCleanup check if any local VMs are still running. If not, it will
// check if a pid exists in the config file and if the process is running
// and kill it/remove the PID file via the ServerKill function above.
func (m *Local) ServerCleanup() error {
	// TODO: check if any other local VMs are running
	// load pid file
	pidFile, err := ServerPIDFile(m.runtimeDir)
	if err != nil {
		return fmt.Errorf("Failed to load local server PID file: %w", err)
	}
	return ServerKill(pidFile)
}

// Server represents the local cluster background server.
type Server struct {
	pidFile         *pid.PIDFile
	depsCacheDir    string
	registryDir     string
	cloudInitDir    string
	s3Dir           string
	oidcKeyPath     string
	addr            string
	httpPort        int
	httpsPort       int
	httpServer      *http.Server
	httpsServer     *http.Server
	ingressServer   *http.Server
	httpListener    net.Listener
	httpsListener   net.Listener
	ingressListener net.Listener
	nstance         *fakeserver.Server
}

// HTTPPort returns the HTTP server port.
func (w *Server) HTTPPort() int {
	return w.httpPort
}

// HTTPSPort returns the HTTPS server port.
func (w *Server) HTTPSPort() int {
	return w.httpsPort
}

func staticFileHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(""))
}

var cloudInitStaticFiles = map[string]bool{
	"meta-data":      true,
	"vendor-data":    true,
	"network-config": true,
}

// NewServer starts local HTTP and HTTPS servers on specific (if specified) or
// fixed available ports and serves sibling endpoints to local VMs and clients:
//
//	/deps/        — file server over <DepsCacheDir>
//	/cloud-init/  — NoCloud user-data + static datasource files
//	/oidc/        — fake OIDC issuer (discovery, JWKS, /token)
//	/s3/data/     — fake S3 for durable local-cluster buckets
//	/s3/cache/    — fake S3 for cache-backed buckets
//	/vault/       — fake Vault/OpenBao API for local Secrets Store CSI usage (HTTPS)
//	fake Nstance gRPC services on dedicated random ports, published via PID metadata
//
// It also creates a PID file to prevent multiple local servers from
// running and to allow other CLI processes to ensure the server is running and
// determine its port numbers.
func NewServer(pidFile pid.PIDFile, c ConfigSource, addr string, port int, vaultStore fakevault.Store) (*Server, error) {
	if pid := pidFile.PID(); pid != 0 {
		return nil, fmt.Errorf("PID file exists but process is not running: %d", pid)
	}
	if vaultStore == nil {
		return nil, fmt.Errorf("vaultStore must be set")
	}

	// validate the address and port
	if addr == "" {
		return nil, fmt.Errorf("invalid address: %s", addr)
	}
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", port)
	}

	// Create the HTTP listener on a specific (if specified) or random available port.
	httpListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, port))
	if err != nil {
		return nil, fmt.Errorf("failed to create local HTTP listener: %w", err)
	}

	// Create the guest-facing HTTPS listener on a random available port. The
	// selected port is published through the PID metadata as part of oidc_issuer.
	httpsListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, 0))
	if err != nil {
		httpListener.Close()
		return nil, fmt.Errorf("failed to create local HTTPS listener: %w", err)
	}

	// Create the local ingress listener last; ingress traffic is a separate
	// localhost-only path from the VM-facing HTTP/HTTPS services.
	ingressListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localIngressHTTPSPort))
	if err != nil {
		httpListener.Close()
		httpsListener.Close()
		return nil, fmt.Errorf("failed to create local ingress TLS listener on 127.0.0.1:%d: %w", localIngressHTTPSPort, err)
	}

	// Create initial server struct.
	depsManager := deps.NewManager(c.DepsBaseURL(), c.DepsCacheDir())
	w := &Server{
		pidFile:         &pidFile,
		depsCacheDir:    c.DepsCacheDir(),
		registryDir:     depsManager.RegistryCacheDir(),
		cloudInitDir:    filepath.Join(c.DataDirectory(), "local"),
		s3Dir:           filepath.Join(c.DataDirectory(), "s3"),
		oidcKeyPath:     filepath.Join(c.DataDirectory(), "local-server", "oidc-key.pem"),
		addr:            addr,
		httpListener:    httpListener,
		httpsListener:   httpsListener,
		ingressListener: ingressListener,
	}
	w.httpsPort = httpsListener.Addr().(*net.TCPAddr).Port
	if port == 0 {
		w.httpPort = httpListener.Addr().(*net.TCPAddr).Port
	} else {
		w.httpPort = port
	}

	// The fake Nstance server is not mounted under the HTTP(S) muxes: nstance-agent
	// talks to real gRPC registration/agent endpoints. Keep the HTTP server as
	// the parent process so local start/stop lifecycle remains unchanged.
	nstanceStore, err := newLocalNstanceStore(filepath.Join(c.DataDirectory(), "nstance-fake"))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fake nstance store: %w", err)
	}
	nstanceServer, err := fakeserver.New(fakeserver.Config{
		Store:         nstanceStore,
		ClusterID:     "podplane-local",
		ShardID:       "local",
		ListenAddr:    "127.0.0.1:0",
		AdvertiseHost: localGuestHostAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build fake nstance server: %w", err)
	}
	if err := nstanceServer.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to start fake nstance server: %w", err)
	}
	w.nstance = nstanceServer
	_, oidcCertFile, oidcKeyFile, err := ensureLocalOIDCCertificate(c.DataDirectory(), localGuestHostAddr, "127.0.0.1", "localhost", localOIDCHostname)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare local OIDC TLS certificate: %w", err)
	}

	// Build the sibling HTTP and HTTPS handlers.
	httpMux := http.NewServeMux()

	// /deps/ — file server over depsCacheDir.
	httpMux.Handle("/deps/", http.StripPrefix("/deps/", http.FileServer(http.Dir(w.depsCacheDir))))

	// /cloud-init/ — NoCloud user-data + static datasource files.
	cloudInitFileServer := http.FileServer(http.Dir(w.cloudInitDir))
	httpMux.Handle("/cloud-init/", http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if _, ok := cloudInitStaticFiles[filepath.Base(r.URL.Path)]; ok {
			staticFileHandler(rw, r)
			return
		}
		http.StripPrefix("/cloud-init/", cloudInitFileServer).ServeHTTP(rw, r)
	}))

	// /oidc/ — fake OIDC issuer.
	oidcKey, err := oidcserver.LoadOrCreateKeypair(w.oidcKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load oidc keypair: %w", err)
	}
	oidcIssuerURL := fmt.Sprintf("https://%s:%d/oidc", localOIDCHostname, w.httpsPort)
	oidcHandler, err := oidcserver.Handler(oidcIssuerURL, oidcKey, func(clientID string) error {
		if err := clusterconfig.ValidateClusterID(clientID); err != nil {
			return err
		}
		if _, err := os.Stat(ClusterConfigPath(c.DataDirectory(), clientID)); err != nil {
			return fmt.Errorf("local cluster %q is not configured", clientID)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build oidc handler: %w", err)
	}
	httpMux.Handle("/oidc/", http.StripPrefix("/oidc", oidcHandler))
	httpsMux := http.NewServeMux()
	httpsMux.Handle("/oidc/", http.StripPrefix("/oidc", oidcHandler))

	// /s3/data/ — fake S3 for durable local-cluster buckets.
	s3Handler, err := s3fake.Handler(w.s3Dir)
	if err != nil {
		return nil, fmt.Errorf("failed to build s3 handler: %w", err)
	}
	httpMux.Handle("/s3/data/", http.StripPrefix("/s3/data", s3Handler))

	// /s3/cache/ — fake S3 for cache-backed buckets.
	s3CacheHandler, err := s3fake.BucketHandler("registry", w.registryDir)
	if err != nil {
		return nil, fmt.Errorf("failed to build cache s3 handler: %w", err)
	}
	httpMux.Handle("/s3/cache/", http.StripPrefix("/s3/cache", s3CacheHandler))

	// /vault/ — fake Vault/OpenBao API for local Secrets Store CSI usage.
	validator := &fakevault.KubernetesTokenValidator{
		KubernetesAPIURL: hostForwardedKubernetesAPIURL(c.RuntimeDirectory()),
		Client: &http.Client{
			Timeout:   5 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
	fakeVault := fakevault.NewHandler(vaultStore, validator.ValidateToken)
	httpsMux.Handle("/vault/", fakeVault)

	// Create the guest-facing HTTP and HTTPS servers with an app-level peer allowlist.
	w.httpServer = &http.Server{Handler: localServicePeerAllowlist(httpMux)}
	w.httpsServer = &http.Server{Handler: localServicePeerAllowlist(httpsMux)}

	// Configure local proxy using dynamically selected mkcert-generated TLS certificates.
	ingressTLSConfig := localIngressTLSConfig(c.DataDirectory())
	w.ingressServer = &http.Server{
		Handler:   localIngressProxy(c.RuntimeDirectory()),
		TLSConfig: ingressTLSConfig,
	}

	// Start the HTTP, HTTPS, and ingress servers in goroutines.
	go func() {
		if err := w.httpServer.Serve(w.httpListener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Local HTTP server error: %v\n", err)
		}
	}()
	go func() {
		if err := w.httpsServer.ServeTLS(w.httpsListener, oidcCertFile, oidcKeyFile); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Local HTTPS server error: %v\n", err)
		}
	}()
	go func() {
		if err := w.ingressServer.ServeTLS(w.ingressListener, "", ""); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Local ingress proxy error: %v\n", err)
		}
	}()

	// write pid file
	if err := w.pidFile.SetPID(os.Getpid()); err != nil {
		return nil, fmt.Errorf("failed to set local server PID: %w", err)
	}
	w.pidFile.SetData("dir", w.depsCacheDir)
	w.pidFile.SetData("host", w.addr)
	w.pidFile.SetData("http_port", fmt.Sprintf("%d", w.httpPort))
	w.pidFile.SetData("https_port", fmt.Sprintf("%d", w.httpsPort))
	w.pidFile.SetData("oidc_issuer", oidcIssuerURL)
	w.pidFile.SetData("ingress_https_port", fmt.Sprintf("%d", localIngressHTTPSPort))
	w.pidFile.SetData("log_file", ServerLogPath(c.RuntimeDirectory()))
	regAddr, agentAddr := nstanceServer.Addr()
	w.pidFile.SetData("nstance_registration_addr", regAddr)
	w.pidFile.SetData("nstance_agent_addr", agentAddr)
	err = w.pidFile.Write()
	if err != nil {
		return w, fmt.Errorf("failed to write PID file: %w", err)
	}

	return w, nil
}

// localServicePeerAllowlist restricts the guest-facing local HTTP(S) servers to
// peers that are expected to be host-local. Under QEMU user networking, guest
// requests to localGuestHostAddr are seen by the host service as 127.0.0.1.
func localServicePeerAllowlist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}

// Stop stops the local HTTP, HTTPS, and ingress servers.
func (w *Server) Stop(timeout time.Duration) error {
	// return if the HTTP server is not running
	if w.httpServer == nil {
		return nil
	}

	// Shutdown the local servers gracefully.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := w.httpServer.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("failed to shutdown local HTTP server: %w", err)
	}
	if w.httpsServer != nil {
		if err := w.httpsServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown local HTTPS server: %w", err)
		}
	}
	if w.ingressServer != nil {
		if err := w.ingressServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown local ingress proxy: %w", err)
		}
	}
	if w.nstance != nil {
		if err := w.nstance.Stop(ctx); err != nil {
			return fmt.Errorf("failed to shutdown fake nstance server: %w", err)
		}
	}

	// Remove the PID file
	return w.pidFile.Clean()
}
