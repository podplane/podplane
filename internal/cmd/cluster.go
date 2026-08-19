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
	Short: "Manage cluster infrastructure",
	Long:  "Commands to generate OpenTofu/Terraform files and manage Podplane cluster infrastructure.",
}

func newClusterCmd(c *config.Config) *cobra.Command {
	clusterCmd.Run = func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	}
	clusterCmd.AddCommand(newClusterCreateCmd(c))
	clusterCmd.AddCommand(newClusterUpgradeCmd(c))
	clusterCmd.AddCommand(newClusterDeleteCmd(c))
	return clusterCmd
}
