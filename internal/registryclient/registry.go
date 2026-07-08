// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package registryclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/kubectl"
)

const (
	zotRegistryNamespace = "platform-zot-registry"
	zotRegistryService   = "zot-registry"
	zotRegistryPort      = "5000"
)

// ensureZotRegistryReady checks that the zot-registry Service has ready endpoints.
func ensureZotRegistryReady(kubeContext, kubeconfig string) error {
	args := append(kubectl.Args(kubeContext, kubeconfig), "-n", zotRegistryNamespace, "get", "endpoints", zotRegistryService, "-o", "json")
	var stdout, stderr bytes.Buffer
	cmd := execwrap.Command("kubectl", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zot-registry is not installed or has no Service endpoints; install it with `podplane install zot-registry`: %s", strings.TrimSpace(stderr.String()))
	}
	var endpoints struct {
		Subsets []struct {
			Addresses []any `json:"addresses"`
		} `json:"subsets"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &endpoints); err != nil {
		return fmt.Errorf("parse zot-registry endpoints: %w", err)
	}
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			return nil
		}
	}
	return fmt.Errorf("zot-registry is installed but has no ready Service endpoints; install or repair it with `podplane install zot-registry`")
}

// startRegistryPortForward opens a local port-forward to the zot-registry Service.
func startRegistryPortForward(ctx context.Context, kubeContext, kubeconfig string, output io.Writer, verbose bool) (string, func(), error) {
	port, err := freeLocalPort()
	if err != nil {
		return "", nil, err
	}
	args := append(kubectl.Args(kubeContext, kubeconfig), "-n", zotRegistryNamespace, "port-forward", "--address", "127.0.0.1", "svc/"+zotRegistryService, port+":"+zotRegistryPort)
	cmd := execwrap.Command("kubectl", args...)
	pfStdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	pfStderr, err := cmd.StderrPipe()
	if err != nil {
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start registry port-forward: %w", err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	ready := make(chan error, 1)
	go waitForPortForwardReady([]io.Reader{pfStdout, pfStderr}, output, verbose, ready)
	select {
	case err := <-ready:
		if err != nil {
			stop()
			return "", nil, err
		}
		return port, stop, nil
	case <-time.After(15 * time.Second):
		stop()
		return "", nil, fmt.Errorf("timed out waiting for registry port-forward")
	case <-ctx.Done():
		stop()
		return "", nil, ctx.Err()
	}
}

// waitForPortForwardReady watches kubectl port-forward output until it reports
// a ready forwarding line or all output streams close before readiness. Routine
// kubectl port-forward chatter is suppressed by default, while unexpected lines
// are still surfaced and all lines are retained for startup failure diagnostics.
func waitForPortForwardReady(readers []io.Reader, output io.Writer, verbose bool, ready chan<- error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	reported := false
	var transcript bytes.Buffer
	report := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if reported {
			return
		}
		reported = true
		if err != nil {
			if lines := strings.TrimSpace(transcript.String()); lines != "" {
				err = fmt.Errorf("%w:\n%s", err, lines)
			}
		}
		ready <- err
	}

	wg.Add(len(readers))
	for _, reader := range readers {
		go func(reader io.Reader) {
			defer wg.Done()
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				line := scanner.Text()
				mu.Lock()
				_, _ = fmt.Fprintln(&transcript, line)
				mu.Unlock()
				if output != nil && (verbose || (!strings.Contains(line, "Forwarding from") && !strings.Contains(line, "Handling connection for"))) {
					_, _ = fmt.Fprintln(output, line)
				}
				if strings.Contains(line, "Forwarding from") {
					report(nil)
				}
			}
			if err := scanner.Err(); err != nil {
				report(err)
			}
		}(reader)
	}
	wg.Wait()
	report(fmt.Errorf("registry port-forward exited before becoming ready"))
}

// freeLocalPort reserves and releases an available loopback TCP port.
func freeLocalPort() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate local port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return "", err
	}
	return port, nil
}
