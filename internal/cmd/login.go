// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/podplane/podplane/internal/clusterauth"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/kubectl"
	"github.com/spf13/cobra"
)

var (
	loginClusterConfig string
	loginCACert        string
	loginCallbackPort  int
	loginHeadless      bool
)

// defaultClusterConfigName is the file looked up in the working directory
// when -f/--cluster-config is omitted.
const defaultClusterConfigName = "podplane.cluster.jsonc"

// newLoginCmd creates the `podplane login` command. Login is only used to
// authenticate against remote clusters — local clusters are configured
// automatically by `podplane local start`.
func newLoginCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to a Podplane cluster",
		Long: `Authenticate to a Podplane cluster using OIDC auth-code + PKCE.

The cluster is described by a .cluster.jsonc file. By default the CLI
looks for it at ./podplane.cluster.jsonc; pass -f to point at a different file.

On success the resulting tokens are stored (id_token + refresh_token in the
OS keyring; metadata in the config file) and kubectl is configured with a
matching cluster, user and context.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath := loginClusterConfig
			if cfgPath == "" {
				cfgPath = defaultClusterConfigName
			}
			cfgPathInput := cfgPath
			cfgPath, err := filepath.Abs(cfgPathInput)
			if err != nil {
				return fmt.Errorf("resolve %s: %w", cfgPathInput, err)
			}
			if _, err := os.Stat(cfgPath); err != nil {
				return fmt.Errorf("cluster config %s: %w", cfgPath, err)
			}
			cluster, err := clusterconfig.Load(cfgPath)
			if err != nil {
				return err
			}
			httpClient, err := clusterauth.NewOIDCHTTPClient(c, cluster)
			if err != nil {
				return err
			}
			kubeAPICAPath, err := c.ResolveCACert("cluster-ca", loginCACert)
			if err != nil {
				return fmt.Errorf("resolve kubernetes api ca cert: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			meta, _, err := clusterauth.Login(ctx, c, clusterauth.Options{
				Cluster:      cluster,
				HTTPClient:   httpClient,
				CallbackPort: loginCallbackPort,
				Headless:     loginHeadless,
			})
			if err != nil {
				return err
			}
			if err := kubectl.ConfigureClusterAccess(os.Stdout, cluster.Cluster.ID, cluster.ResolvedKubernetesAPIURL(), meta.Sub, kubeAPICAPath, false); err != nil {
				return err
			}
			if err := c.SetClusterSummary(config.ClusterSummaryFromConfig(cluster), false); err != nil {
				return fmt.Errorf("cache cluster summary: %w", err)
			}
			user := meta.UserEmail
			if user == "" {
				user = meta.Sub
			}
			fmt.Printf("✓ Logged in to cluster %q as %s\n", cluster.Cluster.ID, user)
			return nil
		},
	}
	cmd.Flags().StringVarP(&loginClusterConfig, "cluster-config", "f", "", "Path to a podplane.cluster.jsonc file (default: ./podplane.cluster.jsonc)")
	cmd.Flags().StringVar(&loginCACert, "ca-cert", "", "Path/URL/inline-PEM for the Kubernetes API server CA certificate")
	cmd.Flags().IntVar(&loginCallbackPort, "callback-port", 8000, "Port for the local OIDC callback HTTP server")
	cmd.Flags().BoolVar(&loginHeadless, "headless", false, "Skip opening a browser; follow the authorize redirect non-interactively")
	return cmd
}
