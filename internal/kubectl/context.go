// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// SetContext first uses kubectl to check if a context with the same key already
// exists (and early exits if so), then runs kubectl to set a context
func SetContext(stdout io.Writer, sub string, clusterID string, local bool) error {
	// build kubectl keys
	key := ContextKey(clusterID, local)
	clusterKey := ClusterKey(clusterID, local)
	credentialsKey := CredentialsKey(sub, clusterID, local)
	// run kubectl to check if context already exists
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd := execwrap.Command(
		"kubectl",
		"config",
		"view",
		"--output=jsonpath={.contexts[?(@.name==\""+key+"\")].context}",
	)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errBuf.String())
		// Handle the specific JSONPath error when contexts array is nil (first-time setup)
		if strings.Contains(stderr, "is not array or slice and cannot be filtered") {
			// This is expected for first-time setup, continue with context configuration
		} else {
			return fmt.Errorf("error invoking kubectl command: %s", err)
		}
	} else {
		// parse json output
		outString := strings.TrimSpace(outBuf.String())
		var outJson struct {
			Cluster string `json:"cluster"`
			User    string `json:"user"`
		}
		err := json.Unmarshal([]byte(outString), &outJson)
		if err == nil {
			if outJson.Cluster != clusterKey {
				fmt.Fprintf(stdout, "Skipping configuration of kubectl context '%s' due to cluster mismatch.\n", key)
				return nil
			} else if outJson.User == credentialsKey {
				fmt.Fprintf(stdout, "Context already exist for %s\n", key)
				return nil
			}
		}
	}
	// run kubectl to configure context
	cmd = execwrap.Command(
		"kubectl",
		"config",
		"set-context",
		key,
		"--cluster="+clusterKey,
		"--user="+credentialsKey,
		"--namespace=default",
	)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ClusterIDFromContext returns a Podplane cluster ID from a kubeconfig
// context. If kubeContext is empty, the current context is used.
func ClusterIDFromContext(kubeContext, kubeconfig string) (string, error) {
	clusterID, local, err := clusterIDFromContext(kubeContext, kubeconfig)
	if err != nil {
		return "", err
	}
	if local {
		name := strings.TrimSpace(kubeContext)
		if name == "" {
			name = ContextKey(clusterID, true)
		}
		return "", fmt.Errorf("context %q is a local Podplane cluster; use `podplane local stop` or `podplane local delete`", name)
	}
	return clusterID, nil
}

// LocalClusterIDFromContext returns a local Podplane cluster ID from a
// kubeconfig context. If kubeContext is empty, the current context is used.
func LocalClusterIDFromContext(kubeContext, kubeconfig string) (string, error) {
	clusterID, local, err := clusterIDFromContext(kubeContext, kubeconfig)
	if err != nil {
		return "", err
	}
	if !local {
		return "", fmt.Errorf("context %q is not a local Podplane cluster", kubeContext)
	}
	return clusterID, nil
}

// clusterIDFromContext returns a kubeconfig context's Podplane cluster ID and
// whether it is a local Podplane cluster context.
func clusterIDFromContext(kubeContext, kubeconfig string) (string, bool, error) {
	args := []string{"config", "view", "--raw", "--output=json"}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd := execwrap.Command("kubectl", args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		stderr := strings.TrimSpace(errOut.String())
		if stderr != "" {
			return "", false, fmt.Errorf("read kubeconfig: %w: %s", err, stderr)
		}
		return "", false, fmt.Errorf("read kubeconfig: %w", err)
	}

	var cfg struct {
		CurrentContext string `json:"current-context"`
		Contexts       []struct {
			Name    string `json:"name"`
			Context struct {
				Cluster string `json:"cluster"`
			} `json:"context"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(out.Bytes(), &cfg); err != nil {
		return "", false, fmt.Errorf("parse kubeconfig: %w", err)
	}

	name := strings.TrimSpace(kubeContext)
	if name == "" {
		name = strings.TrimSpace(cfg.CurrentContext)
	}
	if name == "" {
		return "", false, fmt.Errorf("no kubeconfig context selected; pass --context, --cluster, or -f/--cluster-config")
	}

	for _, context := range cfg.Contexts {
		if context.Name != name {
			continue
		}
		if strings.HasPrefix(context.Context.Cluster, "podplane-local-") {
			return strings.TrimPrefix(context.Context.Cluster, "podplane-local-"), true, nil
		}
		if clusterID := strings.TrimPrefix(context.Context.Cluster, "podplane-"); clusterID != context.Context.Cluster {
			return clusterID, false, nil
		}
		return name, false, nil
	}
	return "", false, fmt.Errorf("kubeconfig context %q not found", name)
}
