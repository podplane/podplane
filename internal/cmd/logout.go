// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/podplane/podplane/internal/clusterauth"
	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/kubectl"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

var (
	logoutClusterConfig string
	logoutCluster       string
	logoutContext       string
	logoutKubeconfig    string
	logoutAutoApprove   bool
)

// newLogoutCmd creates the `podplane logout` command.
//
// Logout clears local state only — it does not revoke tokens at the issuer.
func newLogoutCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear cached credentials for a Podplane cluster",
		Long: `Clear cached credentials for a Podplane cluster.

Removes both the metadata stored in the config file and the tokens stored in
the OS keyring for every (sub, cluster) pair matching the resolved cluster,
then removes the matching kubectl user, cluster, and context entries. Does not
revoke the tokens at the issuer.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			clusterID := strings.TrimSpace(logoutCluster)
			if clusterID == "" && logoutClusterConfig != "" {
				cfg, err := clusterconfig.Load(logoutClusterConfig)
				if err != nil {
					return err
				}
				clusterID = cfg.Cluster.ID
			}
			if clusterID == "" {
				var err error
				var local bool
				clusterID, local, err = kubectl.ClusterIDFromContext(logoutContext, logoutKubeconfig)
				if err != nil {
					return err
				}
				if local {
					name := strings.TrimSpace(logoutContext)
					if name == "" {
						name = kubectl.ContextKey(clusterID, true)
					}
					return fmt.Errorf("context %q is a local Podplane cluster; use `podplane local stop` or `podplane local delete`", name)
				}
			}
			if err := clusterconfig.ValidateClusterID(clusterID); err != nil {
				return fmt.Errorf("invalid cluster ID %q: %w", clusterID, err)
			}

			ok, err := tui.Confirm(fmt.Sprintf("Log out of Podplane cluster %q?", clusterID), logoutAutoApprove, 0)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("logout cancelled")
			}
			return clusterauth.Logout(c, os.Stdout, clusterID, false)
		},
	}
	cmd.Flags().StringVarP(&logoutClusterConfig, "cluster-config", "f", "", "Path to a podplane.cluster.jsonc file")
	cmd.Flags().StringVar(&logoutCluster, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&logoutContext, "context", "", "The name of the kubeconfig context to use")
	cmd.Flags().StringVar(&logoutKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().BoolVarP(&logoutAutoApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	return cmd
}
