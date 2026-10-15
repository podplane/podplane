// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// DeleteClusterAccess removes the kubeconfig context, cluster, and user entries
// created by ConfigureClusterAccess. Missing entries are ignored so cleanup can
// safely run after partial setup or repeated delete/logout attempts.
func DeleteClusterAccess(stdout io.Writer, clusterID string, subs []string, local bool) error {
	if clusterID == "" {
		return fmt.Errorf("kubectl cleanup: cluster id is required")
	}

	if err := kubectlConfigDelete(stdout, "delete-context", ContextKey(clusterID, local)); err != nil {
		return fmt.Errorf("delete kubectl context: %w", err)
	}
	if err := kubectlConfigDelete(stdout, "delete-cluster", ClusterKey(clusterID, local)); err != nil {
		return fmt.Errorf("delete kubectl cluster: %w", err)
	}

	seen := map[string]bool{}
	for _, sub := range subs {
		sub = strings.TrimSpace(sub)
		if sub == "" || seen[sub] {
			continue
		}
		seen[sub] = true
		if err := kubectlConfigDelete(stdout, "delete-user", CredentialsKey(sub, clusterID, local)); err != nil {
			return fmt.Errorf("delete kubectl user: %w", err)
		}
	}

	return nil
}

// kubectlConfigDelete removes a kubeconfig entry, ignoring missing entries and logging successful output.
func kubectlConfigDelete(stdout io.Writer, subcommand, key string) error {
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd := execwrap.Command("kubectl", "config", subcommand, key)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		if strings.Contains(stderr, "not in ") {
			return nil
		}
		if stderr != "" {
			return fmt.Errorf("%s %s: %w: %s", subcommand, key, err, stderr)
		}
		return fmt.Errorf("%s %s: %w", subcommand, key, err)
	}
	if out := strings.TrimSpace(outBuf.String()); out != "" {
		slog.Debug("kubectl config delete", "command", subcommand, "key", key, "output", out)
	}
	return nil
}
