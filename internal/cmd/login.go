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
	"github.com/podplane/podplane/internal/dockerconfig"
	"github.com/podplane/podplane/internal/kubectl"
	"github.com/podplane/podplane/internal/oidc"
	"github.com/spf13/cobra"
)

var (
	loginClusterConfig    string
	loginCACert           string
	loginCallbackPort     int
	loginHeadless         bool
	loginIdentityProvider string
	loginIdentityFile     string
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
		Short: "Log in to a Podplane cluster",
		Long: `Log in to a Podplane cluster and configure kubectl.

Podplane opens your browser for a user login, or uses a short-lived identity
token when running as a service in GitHub Actions or Buildkite.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := oidc.SelectSource(oidc.SourceOptions{IdentityProvider: loginIdentityProvider, IdentityFile: loginIdentityFile})
			if err != nil {
				return err
			}
			cfgPath := loginClusterConfig
			if cfgPath == "" {
				cfgPath = defaultClusterConfigName
			}
			cfgPathInput := cfgPath
			cfgPath, err = filepath.Abs(cfgPathInput)
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
			var meta config.AuthMetadata
			if source.IsUserLogin() {
				meta, _, err = clusterauth.Login(ctx, c, clusterauth.Options{
					Cluster:      cluster,
					HTTPClient:   httpClient,
					CallbackPort: loginCallbackPort,
					Headless:     loginHeadless,
				})
			} else {
				var identityToken string
				identityToken, err = oidc.Acquire(ctx, httpClient, source, cluster.ResolvedClientID())
				if err == nil {
					meta, _, err = clusterauth.ServiceLogin(ctx, c, cluster, httpClient, source, identityToken)
				}
			}
			if err != nil {
				return err
			}
			if err := kubectl.ConfigureClusterAccess(os.Stdout, cluster.Cluster.ID, cluster.ResolvedKubernetesAPIURL(), meta.Sub, kubeAPICAPath, false); err != nil {
				return err
			}
			if err := c.SetClusterSummary(config.ClusterSummaryFromConfig(cluster), false); err != nil {
				return fmt.Errorf("cache cluster summary: %w", err)
			}
			if cluster.Cluster.Registry.Hostname != "" && cluster.Cluster.Registry.Ingress.Enabled {
				configured, err := dockerconfig.SetCredentialHelper(cluster.Cluster.Registry.Hostname)
				if err != nil {
					return err
				}
				if configured {
					fmt.Printf("✓ Configured Docker credential helper for %s\n", cluster.Cluster.Registry.Hostname)
				}
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
	cmd.Flags().StringVar(&loginIdentityProvider, "identity-provider", "", "Identity provider: github, buildkite, or none (default: detect automatically)")
	cmd.Flags().StringVar(&loginIdentityFile, "identity-file", "", "Read an identity token from PATH")
	return cmd
}
