// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
				_, _ = fmt.Fprintf(stdout, "Skipping configuration of kubectl context '%s' due to cluster mismatch.\n", key)
				return nil
			} else if outJson.User == credentialsKey {
				_, _ = fmt.Fprintf(stdout, "Context already exist for %s\n", key)
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

// Context identifies a selected kubeconfig context and its referenced resources.
type Context struct {
	Name      string `json:"-"`
	Cluster   string `json:"cluster"`
	User      string `json:"user"`
	Namespace string `json:"namespace"`
}

// ResolveContext reads the explicit or current kubeconfig context once. Callers
// use its Name for subsequent requests rather than resolving the current context again.
func ResolveContext(ctx context.Context, kubeContext, kubeconfig string) (Context, error) {
	if err := ctx.Err(); err != nil {
		return Context{}, err
	}
	args := append(Args(strings.TrimSpace(kubeContext), kubeconfig), "config", "view", "--output=json")

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Context{}, ctx.Err()
		}
		stderr := strings.TrimSpace(errOut.String())
		if stderr != "" {
			return Context{}, fmt.Errorf("read kubeconfig: %w: %s", err, stderr)
		}
		return Context{}, fmt.Errorf("read kubeconfig: %w", err)
	}

	var cfg struct {
		CurrentContext string `json:"current-context"`
		Contexts       []struct {
			Name    string  `json:"name"`
			Context Context `json:"context"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(out.Bytes(), &cfg); err != nil {
		return Context{}, fmt.Errorf("parse kubeconfig: %w", err)
	}

	name := strings.TrimSpace(kubeContext)
	if name == "" {
		name = strings.TrimSpace(cfg.CurrentContext)
	}
	if name == "" {
		return Context{}, fmt.Errorf("no kubeconfig context selected; pass --context or select a current context")
	}

	for _, entry := range cfg.Contexts {
		if entry.Name != name {
			continue
		}
		entry.Context.Name = entry.Name
		return entry.Context, nil
	}
	return Context{}, fmt.Errorf("kubeconfig context %q not found", name)
}

// ClusterID returns the Podplane cluster ID and whether it is local. For other
// clusters it retains the context name as the ID, matching cached-summary lookup.
func (c Context) ClusterID() (string, bool) {
	if id, ok := strings.CutPrefix(c.Cluster, "podplane-local-"); ok {
		return id, true
	}
	if id, ok := strings.CutPrefix(c.Cluster, "podplane-"); ok {
		return id, false
	}
	return c.Name, false
}

// UserFromContext returns the kubeconfig user for the explicit or current context.
func UserFromContext(kubeContext, kubeconfig string) (string, error) {
	selected, err := ResolveContext(context.Background(), kubeContext, kubeconfig)
	if err != nil {
		return "", err
	}
	return selected.User, nil
}

// ClusterIDFromContext returns the cluster ID and local status for the explicit
// or current context. Call ResolveContext when subsequent requests need to pin its name.
func ClusterIDFromContext(kubeContext, kubeconfig string) (string, bool, error) {
	selected, err := ResolveContext(context.Background(), kubeContext, kubeconfig)
	if err != nil {
		return "", false, err
	}
	id, local := selected.ClusterID()
	return id, local, nil
}
