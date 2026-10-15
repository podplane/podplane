// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/podplane/podplane/internal/deps"
	"github.com/spf13/cobra"
)

var depsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Checks for new dependency versions and reports current cached versions",
	Long:  `Status checks if a new dependency version is available to download, and reports on the current cached versions. Generally you should not be running this directly.`,
}

// newDepsStatusCmd wires manifest checking and prints whether dependency updates are available.
func newDepsStatusCmd(manager *deps.Manager, kind, arch string) *cobra.Command {
	depsStatusCmd.RunE = func(cmd *cobra.Command, args []string) error {
		fmt.Println("Fetching latest dependency manifest...")
		needsUpdating, _, err := manager.Status(kind, arch)
		if err != nil {
			fmt.Printf("Error checking for new packages: %v\n", err)
			return nil
		}
		if needsUpdating {
			fmt.Println("New packages are available to download.")
		} else {
			fmt.Println("No new packages available.")
		}
		return nil
	}

	return depsStatusCmd
}
