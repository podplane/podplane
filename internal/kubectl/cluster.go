// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// SetCluster first uses kubectl to check if a cluster with the same key already
// exists (and early exits if so), then runs kubectl to set a cluster
func SetCluster(stdout io.Writer, clusterID string, kubeApiUrl string, caCertPath string, local bool) error {
	// build kubectl cluster key
	key := ClusterKey(clusterID, local)
	serverURL := kubeApiUrl
	tlsServerName := ""
	if local {
		// Local clusters route Kubernetes API traffic through the Podplane local
		// ingress proxy. Use loopback to avoid relying on host DNS for
		// <cluster>.k8s.localhost, while preserving that hostname for TLS verification.
		if u, err := url.Parse(kubeApiUrl); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".k8s.localhost") {
			tlsServerName = u.Hostname()
			if port := u.Port(); port != "" {
				u.Host = net.JoinHostPort("127.0.0.1", port)
			} else {
				u.Host = "127.0.0.1"
			}
			serverURL = u.String()
		}
	}
	// run kubectl to check if cluster already exists
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd := execwrap.Command(
		"kubectl",
		"config",
		"view",
		"--output=jsonpath={.clusters[?(@.name==\""+key+"\")].cluster.server}",
	)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		// Handle the specific JSONPath error when clusters array is nil (first-time setup)
		if strings.Contains(stderr, "is not array or slice and cannot be filtered") {
			// This is expected for first-time setup, continue with cluster configuration
		} else {
			return fmt.Errorf("Error invoking kubectl command: %s", err)
		}
	} else {
		outString := strings.TrimSpace(outBuf.String())
		if outString == serverURL && (!local || tlsServerName == "") {
			_, _ = fmt.Fprintf(stdout, "Cluster already exist for %s\n", key)
			return nil
		} else if outString != "" && !local && !strings.Contains(outString, "podplane") {
			_, _ = fmt.Fprintf(stdout, "Skipping configuration of kubectl cluster '%s' due to unknown conflict.\n", key)
			return nil
		}
	}
	// run kubectl to configure cluster
	args := []string{"config", "set-cluster", key, "--server=" + serverURL}
	if caCertPath != "" {
		args = append(args, "--certificate-authority="+caCertPath)
	}
	if tlsServerName != "" {
		args = append(args, "--tls-server-name="+tlsServerName)
	}
	cmd = execwrap.Command("kubectl", args...)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
