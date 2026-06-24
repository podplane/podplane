// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/local"
	"github.com/spf13/cobra"
)

var localStatusJSON bool

var localStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check the status of a local cluster VM",
	Long:  `Check the local cluster VM status.`,
}

func newLocalStatusCmd(c *config.Config) *cobra.Command {
	localStatusCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Create local cluster manager and check the VM status
		manager := local.NewManager(c, localClusterID)
		if localStatusJSON {
			report, err := manager.StatusReport()
			if err != nil {
				return fmt.Errorf("failed to check status: %w", err)
			}
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		}
		if err := manager.Status(); err != nil {
			return fmt.Errorf("failed to check status: %w", err)
		}
		return nil
	}
	localStatusCmd.Flags().BoolVar(&localStatusJSON, "json", false, "Print local status as JSON")

	return localStatusCmd
}
