// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/local"
	"github.com/spf13/cobra"
)

var localSyncCmd = &cobra.Command{
	Use:   "sync [from] [to]",
	Short: "Rsync files into the local cluster VM",
	Long:  `Rsync files into the local cluster VM.`,
}

func init() {
	localSyncCmd.Flags().StringVar(&localStartChown, "chown", "", "Rsync chown flag")
	localSyncCmd.Flags().StringArrayVar(&localSyncExcludes, "exclude", nil, "Rsync exclude pattern (may be specified multiple times)")
}

var localStartChown string
var localSyncExcludes []string

// newLocalSyncCmd creates the `local sync` command
func newLocalSyncCmd(c *config.Config) *cobra.Command {
	localSyncCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Check the required rsync version is installed.
		// Note: chown support was added starting from version 3.1.0.
		if err := execwrap.Installed([]string{"rsync"}); err != nil {
			return fmt.Errorf("error checking for runtime dependencies: %w", err)
		}

		// Ensure we have exactly one source and one destination path.
		if len(args) != 2 {
			return fmt.Errorf("exactly one 'from' path and one 'to' path are required")
		}
		fromPath := args[0]
		toPath := args[1]

		manager := local.NewManager(c, localClusterID)
		if err := manager.Sync(fromPath, toPath, localStartChown, localSyncExcludes); err != nil {
			return fmt.Errorf("failed to run rsync: %w", err)
		}
		return nil
	}

	return localSyncCmd
}
