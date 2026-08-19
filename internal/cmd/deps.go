// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/tfdeps"
	"github.com/spf13/cobra"
)

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Manage cached dependency files",
	Long:  `Commands to check the status of and download/cache dependency files.`,
}

// newDepsCmd creates the `deps` command.
func newDepsCmd(c *config.Config) *cobra.Command {
	depsCmd.Run = func(cmd *cobra.Command, args []string) {
		// If no subcommand provided, show help
		_ = cmd.Help()
	}

	manager := deps.NewManager(c.DepsBaseURL(), c.DepsCacheDir())
	kind := c.InstanceKind()
	arch := c.Arch()

	depsCmd.AddCommand(newDepsStatusCmd(manager, kind, arch))
	depsCmd.AddCommand(newDepsDownloadCmd(manager, kind, arch))
	depsCmd.AddCommand(newDepsTFCmd(tfdeps.New(c.DepsCacheDir())))

	return depsCmd
}
