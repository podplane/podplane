// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/nstance-dev/nstance/pkg/fakeserver"

	"github.com/podplane/podplane/internal/oidcserver"
	"github.com/podplane/podplane/internal/vm"
)

const localReadinessTimeout = 10 * time.Minute
const localSystemServicesTimeout = 2 * time.Minute
const localNstanceAgentTimeout = 2 * time.Minute
const localNetsyHealthTimeout = 2 * time.Minute
const localAPIReadinessTimeout = 2 * time.Minute

// ReadinessOptions configures optional output while waiting for first-boot
// user-data to finish.
type ReadinessOptions struct {
	StreamUserdataLogs bool
	Quiet              bool
}

// WaitOptions configures output for simple local readiness waits.
type WaitOptions struct {
	Quiet bool
}

// WaitForReadiness waits for the local VM's first-boot user-data script to
// complete successfully. It intentionally does not wait for Kubernetes here:
// local vmconfig development images may stop after user-data setup and before
// install.sh/configure.sh have been synced and run.
func (m *Local) WaitForReadiness(ctx context.Context, opts ReadinessOptions) error {
	state, err := readState(m.runtimeDir, m.clusterID)
	if err != nil {
		return err
	}
	sshPort := state.Ports.SSH
	if sshPort == 0 {
		return fmt.Errorf("state is missing ssh port")
	}
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}

	deadline := time.Now().Add(localReadinessTimeout)
	var stopProgress func()
	if !opts.StreamUserdataLogs && !opts.Quiet {
		stopProgress = startReadinessProgress()
	}
	var stopStreaming context.CancelFunc
	var streamDone <-chan struct{}
	if opts.StreamUserdataLogs {
		stopStreaming, streamDone = m.streamUserdataLogs(ctx, sshPort, privateKeyPath)
	}
	defer func() {
		if stopProgress != nil {
			stopProgress()
		}
		if stopStreaming != nil {
			stopStreaming()
			<-streamDone
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out waiting for cloud-init user-data after %s; run `podplane local console` to inspect the VM boot and cloud-init logs", localReadinessTimeout)
		}
		commandTimeout := remaining
		if commandTimeout > 30*time.Second {
			commandTimeout = 30 * time.Second
		}
		output, err := m.vm.Shell(ctx, "cloud-init status --wait", sshPort, privateKeyPath, vm.ShellOptions{Timeout: commandTimeout})
		if err == nil {
			trimmed := strings.TrimSpace(string(output))
			if strings.Contains(trimmed, "status: done") || trimmed == "" {
				if stopProgress != nil {
					stopProgress()
					stopProgress = nil
				}
				if !opts.Quiet {
					color.Green("✓ cloud-init user-data completed successfully")
				}
				return nil
			}
			return fmt.Errorf("cloud-init finished with unexpected status: %s", trimmed)
		}
		if strings.Contains(string(output), "status: error") {
			return fmt.Errorf("cloud-init user-data failed: %s", strings.TrimSpace(string(output)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// WaitForSystemServices waits for core guest services that kube-apiserver
// depends on. It intentionally does not check kube-apiserver itself;
// WaitForAPIServer performs the subsequent /livez and /readyz probes.
func (m *Local) WaitForSystemServices(ctx context.Context, opts WaitOptions) error {
	state, err := readState(m.runtimeDir, m.clusterID)
	if err != nil {
		return err
	}
	sshPort := state.Ports.SSH
	if sshPort == 0 {
		return fmt.Errorf("state is missing ssh port")
	}
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}

	services := "containerd nstance-agent netsy"
	deadline := time.Now().Add(localSystemServicesTimeout)
	if !opts.Quiet {
		fmt.Println("Waiting for system services to start...")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			status, _ := m.vm.Shell(ctx, "systemctl --no-pager --plain is-active "+services, sshPort, privateKeyPath, vm.ShellOptions{Timeout: 5 * time.Second})
			return fmt.Errorf("timed out waiting for system services after %s: %s", localSystemServicesTimeout, strings.TrimSpace(string(status)))
		}
		commandTimeout := remaining
		if commandTimeout > 10*time.Second {
			commandTimeout = 10 * time.Second
		}
		_, err := m.vm.Shell(ctx, "systemctl is-active --quiet "+services, sshPort, privateKeyPath, vm.ShellOptions{Timeout: commandTimeout})
		if err == nil {
			if !opts.Quiet {
				color.Green("✓ system services started")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// WaitForNstanceAgentRegistration waits until nstance-agent has registered with
// the local fake Nstance server. systemd can report nstance-agent as active
// before the one-time registration handshake finishes, so this is a separate
// readiness boundary from WaitForSystemServices.
func (m *Local) WaitForNstanceAgentRegistration(ctx context.Context, opts WaitOptions) error {
	if m.instanceID == "" {
		return fmt.Errorf("instance ID is required")
	}

	store, err := newLocalNstanceStore(filepath.Join(m.dataDir, "nstance-fake"))
	if err != nil {
		return err
	}
	instanceKey := filepath.ToSlash(filepath.Join("fakeserver", "instances", m.instanceID, "instance.json"))
	deadline := time.Now().Add(localNstanceAgentTimeout)
	if !opts.Quiet {
		fmt.Println("Waiting for Nstance agent registration...")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out waiting for nstance-agent registration after %s", localNstanceAgentTimeout)
		}
		data, err := store.Get(ctx, instanceKey)
		if err == nil {
			var instance struct {
				Registered bool `json:"registered"`
			}
			if err := json.Unmarshal(data, &instance); err != nil {
				return fmt.Errorf("decode fake nstance instance state: %w", err)
			}
			if !instance.Registered {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
				continue
			}
			if !opts.Quiet {
				color.Green("✓ Nstance agent registered")
			}
			return nil
		} else if !errors.Is(err, fakeserver.ErrNotFound) {
			return fmt.Errorf("read fake nstance instance state: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// WaitForNetsyHealth waits until the Netsy HTTP health endpoint reports a
// healthy node state. This follows the systemd check because the netsy service
// can be active while Netsy is still loading its persisted state.
func (m *Local) WaitForNetsyHealth(ctx context.Context, opts WaitOptions) error {
	state, err := readState(m.runtimeDir, m.clusterID)
	if err != nil {
		return err
	}
	sshPort := state.Ports.SSH
	if sshPort == 0 {
		return fmt.Errorf("state is missing ssh port")
	}
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}

	deadline := time.Now().Add(localNetsyHealthTimeout)
	if !opts.Quiet {
		fmt.Println("Waiting for Netsy to become healthy...")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			health, _ := m.vm.Shell(ctx, "curl -fsS --max-time 5 http://127.0.0.1:8080/health || true", sshPort, privateKeyPath, vm.ShellOptions{Timeout: 10 * time.Second})
			return fmt.Errorf("timed out waiting for Netsy health after %s: %s", localNetsyHealthTimeout, strings.TrimSpace(string(health)))
		}
		commandTimeout := remaining
		if commandTimeout > 10*time.Second {
			commandTimeout = 10 * time.Second
		}
		_, err := m.vm.Shell(ctx, "curl -fsS --max-time 5 http://127.0.0.1:8080/health", sshPort, privateKeyPath, vm.ShellOptions{Timeout: commandTimeout})
		if err == nil {
			if !opts.Quiet {
				color.Green("✓ Netsy healthy")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// startReadinessProgress prints a simple one-line heartbeat while cloud-init
// readiness is being checked. It returns an idempotent stop function that
// terminates the heartbeat goroutine and moves subsequent output to a new line.
func startReadinessProgress() func() {
	fmt.Print("Waiting for cloud-init user-data to complete...")
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				fmt.Println()
				return
			case <-ticker.C:
				fmt.Print(".")
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(done)
			<-stopped
		})
	}
}

// WaitForAPIServer waits until the local cluster's kube-apiserver is ready to
// serve API requests. It first waits for /livez (process up and serving TLS),
// then for /readyz (post-start hooks like RBAC bootstrap and priority classes
// have completed). Hitting workloads before /readyz passes can produce 5xx /
// connection resets even though TLS handshakes succeed.
func (m *Local) WaitForAPIServer(ctx context.Context) error {
	return m.WaitForAPIServerWithOptions(ctx, APIReadinessOptions{})
}

// APIReadinessOptions configures Kubernetes API readiness output.
type APIReadinessOptions struct {
	Quiet bool
}

// WaitForAPIServerWithOptions waits until the local cluster's kube-apiserver is
// live and ready with caller-controlled output.
func (m *Local) WaitForAPIServerWithOptions(ctx context.Context, opts APIReadinessOptions) error {
	if err := m.waitForAPIServerEndpoint(ctx, "live", opts.Quiet, m.ProbeAPIServerLive); err != nil {
		return err
	}
	return m.waitForAPIServerEndpoint(ctx, "ready", opts.Quiet, m.ProbeAPIServerReady)
}

func (m *Local) waitForAPIServerEndpoint(ctx context.Context, label string, quiet bool, probe func(context.Context) error) error {
	deadline := time.Now().Add(localAPIReadinessTimeout)
	if !quiet {
		fmt.Printf("Waiting for Kubernetes API server to become %s...\n", label)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for kube-apiserver to become %s after %s", label, localAPIReadinessTimeout)
		}
		if err := probe(ctx); err == nil {
			if !quiet {
				color.Green(fmt.Sprintf("✓ Kubernetes API server %s", label))
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// ProbeAPIServerLive mints a local-OIDC token and issues an authenticated
// GET /livez, returning nil only when the apiserver reports itself alive
// (HTTP 200). It proves the apiserver process is up and serving — but not
// that bootstrap post-start hooks have completed; use ProbeAPIServerReady
// for that.
func (m *Local) ProbeAPIServerLive(ctx context.Context) error {
	return m.probeAPIServerEndpoint(ctx, "/livez")
}

// ProbeAPIServerReady mints a local-OIDC token and issues an authenticated
// GET /readyz, returning nil only when the apiserver reports itself ready
// (HTTP 200). Ready implies live. Writes (e.g. CRD creation) before this
// returns can fail with connection resets even though TLS handshakes succeed.
func (m *Local) ProbeAPIServerReady(ctx context.Context) error {
	return m.probeAPIServerEndpoint(ctx, "/readyz")
}

// probeAPIServerEndpoint authenticates with a freshly-minted local-OIDC token
// and GETs the given apiserver endpoint, returning nil only on HTTP 200. The
// local apiserver runs with --anonymous-auth=false so unauthenticated probes
// always return 401; this probe uses the same OIDC signing key the local
// server itself serves, so the token always validates.
func (m *Local) probeAPIServerEndpoint(ctx context.Context, path string) error {
	state, err := readState(m.runtimeDir, m.clusterID)
	if err != nil {
		return err
	}
	port := state.Ports.KubernetesAPI
	if port == 0 {
		return fmt.Errorf("state is missing kubernetes api port")
	}
	if m.webserverPIDFile.PID() == 0 {
		pidFile, err := ServerPIDFile(m.runtimeDir)
		if err != nil {
			return fmt.Errorf("load local server PID file: %w", err)
		}
		m.webserverPIDFile = pidFile
	}
	issuerURL, err := m.OIDCServerURL(m.vm.Addr())
	if err != nil {
		return fmt.Errorf("derive OIDC issuer URL: %w", err)
	}
	key, err := oidcserver.LoadOrCreateKeypair(m.OIDCKeyPath())
	if err != nil {
		return fmt.Errorf("load OIDC signing key: %w", err)
	}
	token, err := oidcserver.IssueLocalToken(key, issuerURL, m.clusterID)
	if err != nil {
		return fmt.Errorf("mint admin token: %w", err)
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kube-apiserver %s returned %d", path, resp.StatusCode)
	}
	return nil
}

// ProbeCloudInit returns the current cloud-init status string (e.g. "done",
// "running", "error", "not started") from the VM. Unlike WaitForReadiness it
// does not block — it runs `cloud-init status` once and returns immediately.
func (m *Local) ProbeCloudInit(ctx context.Context) (string, error) {
	state, err := readState(m.runtimeDir, m.clusterID)
	if err != nil {
		return "", err
	}
	sshPort := state.Ports.SSH
	if sshPort == 0 {
		return "", fmt.Errorf("state is missing ssh port")
	}
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return "", fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}
	output, err := m.vm.Shell(ctx, "cloud-init status", sshPort, privateKeyPath, vm.ShellOptions{Timeout: 5 * time.Second})
	trimmed := strings.TrimSpace(string(output))
	if err != nil && trimmed == "" {
		return "", err
	}
	// `cloud-init status` prints e.g. "status: done"; strip the prefix.
	if rest, ok := strings.CutPrefix(trimmed, "status:"); ok {
		return strings.TrimSpace(rest), nil
	}
	return trimmed, nil
}

// streamUserdataLogs tails cloud-init output over SSH until the returned cancel
// function is called or the parent context is canceled.
func (m *Local) streamUserdataLogs(ctx context.Context, sshPort int, privateKeyPath string) (context.CancelFunc, <-chan struct{}) {
	streamCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fmt.Print("Waiting for cloud-init log stream..")
		command := "while [ ! -e /var/log/cloud-init-output.log ]; do printf '.'; sleep 1; done; printf '\\n'; tail -n +1 -F /var/log/cloud-init-output.log"
		for {
			_, err := m.vm.Shell(streamCtx, command, sshPort, privateKeyPath, vm.ShellOptions{
				Stdout: os.Stdout,
				Stderr: io.Discard,
			})
			if streamCtx.Err() != nil {
				return
			}
			if err != nil {
				fmt.Print(".")
				select {
				case <-streamCtx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				continue
			}
			select {
			case <-streamCtx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return cancel, done
}
