// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/clusterauth"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/local"
	"github.com/spf13/cobra"
)

var localDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a local cluster",
	Long:  `Delete a running local cluster and its VM`,
}

// newLocalDeleteCmd wires credential cleanup followed by local cluster deletion.
func newLocalDeleteCmd(c *config.Config) *cobra.Command {
	localDeleteCmd.RunE = func(cmd *cobra.Command, args []string) error {
		manager := local.NewManager(c, localClusterID)
		if err := clusterauth.LogoutLocal(os.Stdout, localClusterID); err != nil {
			return fmt.Errorf("failed to clear local cluster credentials: %w", err)
		}
		if err := manager.Delete(); err != nil {
			return fmt.Errorf("failed to delete: %w", err)
		}
		return nil
	}

	return localDeleteCmd
}
