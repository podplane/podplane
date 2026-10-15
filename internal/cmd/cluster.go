// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/podplane/podplane/internal/config"
	"github.com/spf13/cobra"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage clusters",
	Long:  "Commands to manage Podplane clusters and their infrastructure.",
}

// newClusterCmd groups cluster infrastructure lifecycle and sleep commands with fallback help.
func newClusterCmd(c *config.Config) *cobra.Command {
	clusterCmd.Run = func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	}
	clusterCmd.AddCommand(newClusterCreateCmd(c))
	clusterCmd.AddCommand(newClusterUpgradeCmd(c))
	clusterCmd.AddCommand(newClusterDeleteCmd(c))
	clusterCmd.AddCommand(newClusterSleepCmd(c))
	return clusterCmd
}
