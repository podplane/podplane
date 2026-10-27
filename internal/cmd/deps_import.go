// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/podplane/podplane/internal/bundle"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/tui"
)

func newDepsImportCmd(manager *deps.Manager, kind, arch string) *cobra.Command {
	return &cobra.Command{
		Use: "import <directory>", Short: "Verify and import an offline bundle without network access",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := tui.RunDownloadProgress("Importing Podplane dependencies", func(progress func(deps.DownloadEvent)) error {
				return bundle.Import(cmd.Context(), manager, args[0], kind, arch, progress)
			}); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Dependencies imported. Keep the bundle's bin directory on PATH, then run podplane local start --offline.")
			return nil
		},
	}
}
