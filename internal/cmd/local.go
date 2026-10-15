// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/local"
	"github.com/spf13/cobra"
)

var localCmd = &cobra.Command{
	Use:   "local",
	Short: "Manage local clusters",
	Long:  `Commands to manage local clusters. VMs are run using a local provider (e.g. Qemu) which must be installed first.`,
}

// init registers the shared local cluster ID flag.
func init() {
	pflags := localCmd.PersistentFlags()
	pflags.StringVar(&localClusterID, "id", "default", "Cluster ID")
}

var localClusterID string

// newLocalCmd creates the `local` command
func newLocalCmd(c *config.Config) *cobra.Command {
	localCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		// Check runtime dependencies for all local commands
		if err := local.CheckRuntimeDependencies(c.Arch()); err != nil {
			fmt.Println("Error checking for runtime dependencies:", err)
			os.Exit(1)
		}
		// Validate flags
		clusterID := strings.TrimSpace(localClusterID)
		if err := clusterconfig.ValidateClusterID(clusterID); err != nil {
			fmt.Printf("Invalid cluster ID: %v\n", err)
			os.Exit(1)
		}
		localClusterID = clusterID
	}

	localCmd.Run = func(cmd *cobra.Command, args []string) {
		// If no subcommand provided, show help
		_ = cmd.Help()
	}

	// Add subcommands
	localCmd.AddCommand(newLocalServerCmd(c))
	localCmd.AddCommand(newLocalStartCmd(c))
	localCmd.AddCommand(newLocalStatusCmd(c))
	localCmd.AddCommand(newLocalStopCmd(c))
	localCmd.AddCommand(newLocalDeleteCmd(c))
	localCmd.AddCommand(newLocalShellCmd(c))
	localCmd.AddCommand(newLocalConsoleCmd(c))
	localCmd.AddCommand(newLocalSyncCmd(c))

	return localCmd
}
