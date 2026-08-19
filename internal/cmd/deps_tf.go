// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/podplane/podplane/internal/tfdeps"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/spf13/cobra"
)

// newDepsTFCmd builds the OpenTofu/Terraform dependency download command.
func newDepsTFCmd(cache *tfdeps.Cache) *cobra.Command {
	var platforms []string
	cmd := &cobra.Command{
		Use:          "tf",
		Short:        "Download cluster OpenTofu/Terraform dependencies",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer cancel()
			if err := cache.Download(ctx, engine, platforms); err != nil {
				return err
			}
			fmt.Println("Cached cluster OpenTofu/Terraform dependencies")
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&platforms, "platform", nil, "Target provider platform OS_ARCH (repeatable; defaults to the engine platform)")
	return cmd
}
