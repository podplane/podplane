// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/kubectl"
	"github.com/podplane/podplane/internal/secrets"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

var secretFlags struct {
	Provider     string
	For          string
	Namespace    string
	Context      string
	Kubeconfig   string
	Stdin        bool
	File         string
	DryRun       string
	Output       string
	HideArchived bool
	All          bool
	Destroy      bool
	AutoApprove  bool
}

// newSecretCmd builds the top-level singular podplane secret command.
func newSecretCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage application secrets",
		Long:  `Manage application secrets through the Podplane operator secrets API. Secret values are encrypted client-side before they are sent to Kubernetes.`,
	}
	cmd.PersistentFlags().StringVar(&secretFlags.Provider, "provider", "", "Secrets provider name (defaults to the cluster default provider)")
	cmd.PersistentFlags().StringVar(&secretFlags.For, "for", "", "Name of the app deployment SecretProviderClass that scopes the secret")
	cmd.PersistentFlags().StringVarP(&secretFlags.Namespace, "namespace", "n", "", "Kubernetes namespace (defaults to the current context namespace)")
	cmd.PersistentFlags().StringVar(&secretFlags.Context, "context", "", "The name of the kubeconfig context to use")
	cmd.PersistentFlags().StringVar(&secretFlags.Kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	_ = cmd.MarkPersistentFlagRequired("for")

	cmd.AddCommand(
		newSecretCreateCmd(c),
		newSecretUpdateCmd(c),
		newSecretListCmd(c),
		newSecretDeleteCmd(c),
		newSecretRestoreCmd(c),
		newSecretDestroyCmd(c),
	)
	return cmd
}

// secretContext contains the resolved Kubernetes and Podplane secret request scope.
type secretContext struct {
	Namespace    string
	Provider     string
	KeyspaceName string
	ClusterID    string
}

// resolveSecretContext resolves namespace, cluster, provider, and keyspace for a secret command.
func resolveSecretContext(c *config.Config) (secretContext, error) {
	if strings.TrimSpace(secretFlags.For) == "" {
		return secretContext{}, fmt.Errorf("--for is required")
	}
	if err := secrets.ValidateScopeName("--for", secretFlags.For); err != nil {
		return secretContext{}, err
	}
	namespace := strings.TrimSpace(secretFlags.Namespace)
	if namespace == "" {
		var err error
		namespace, err = currentNamespace(secretFlags.Context, secretFlags.Kubeconfig)
		if err != nil {
			return secretContext{}, err
		}
	}
	if err := secrets.ValidateScopeName("--namespace", namespace); err != nil {
		return secretContext{}, err
	}
	clusterID, local, err := kubectl.ClusterIDFromContext(secretFlags.Context, secretFlags.Kubeconfig)
	if err != nil {
		return secretContext{}, err
	}
	summary, err := c.ClusterSummary(clusterID, local)
	if err != nil {
		return secretContext{}, err
	}
	if summary.ID == "" {
		return secretContext{}, fmt.Errorf("cluster summary for %q is not cached; run `podplane login -f <podplane.cluster.jsonc>` for this cluster", clusterID)
	}
	provider, err := secrets.ResolveProvider(summary, secretFlags.Provider)
	if err != nil {
		return secretContext{}, err
	}
	return secretContext{Namespace: namespace, Provider: provider, KeyspaceName: secrets.KeyspaceName(provider, secretFlags.For), ClusterID: summary.ID}, nil
}

// readSecretValue reads a secret value from file, stdin, or an interactive terminal prompt.
func readSecretValue(operation, key string, fromStdin bool, filePath string) ([]byte, error) {
	filePath = strings.TrimSpace(filePath)
	if fromStdin && filePath != "" {
		return nil, fmt.Errorf("--stdin and --file are mutually exclusive")
	}
	if filePath != "" {
		value, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read --file: %w", err)
		}
		return value, nil
	}
	if fromStdin {
		value, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return value, nil
	}
	_, _ = fmt.Fprintf(os.Stderr, "Enter value for %s %q: ", operation, key)
	value, err := term.ReadPassword(int(syscall.Stdin))
	_, _ = fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read secret value: %w", err)
	}
	return value, nil
}

// printKeyspace writes a SecretProviderKeyspace response in the requested format.
func printKeyspace(keyspace secrets.SecretProviderKeyspace, format string) error {
	switch format {
	case "", "yaml":
		data, err := yaml.Marshal(keyspace)
		if err != nil {
			return fmt.Errorf("encode yaml: %w", err)
		}
		fmt.Print(string(data))
		return nil
	case "json":
		data, err := json.MarshalIndent(keyspace, "", "  ")
		if err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		fmt.Println(string(data))
		return nil
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}

// currentNamespace resolves the active Kubernetes namespace for the selected context.
func currentNamespace(kubeContext, kubeconfig string) (string, error) {
	args := append(kubectl.Args(kubeContext, kubeconfig), "config", "view", "--minify", "--output=jsonpath={..namespace}")
	cmd := execwrap.Command("kubectl", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("read current namespace: %w: %s", err, msg)
		}
		return "", fmt.Errorf("read current namespace: %w", err)
	}
	ns := strings.TrimSpace(stdout.String())
	if ns == "" {
		ns = "default"
	}
	return ns, nil
}
