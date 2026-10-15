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

var localStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a local cluster VM",
	Long:  `Stop a running local cluster VM`,
}

// init registers optional cluster deletion after stopping the local VM.
func init() {
	localStopCmd.Flags().BoolVar(&localStopRemove, "rm", false, "Remove (delete) the cluster after stopping")
}

var localStopRemove bool

// newLocalStopCmd wires VM shutdown and optional credential cleanup and cluster deletion.
func newLocalStopCmd(c *config.Config) *cobra.Command {
	localStopCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		// Create local cluster manager and stop the VM
		manager := local.NewManager(c, localClusterID)
		if err := manager.Stop(); err != nil {
			return fmt.Errorf("failed to stop: %w", err)
		}

		// If --rm flag is set, also delete the cluster
		if localStopRemove {
			if err := clusterauth.LogoutLocal(os.Stdout, localClusterID); err != nil {
				return fmt.Errorf("failed to clear local cluster credentials: %w", err)
			}
			if err := manager.Delete(); err != nil {
				return fmt.Errorf("failed to delete after stop: %w", err)
			}
		}

		return nil
	}

	return localStopCmd
}
