// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/infrafiles"
	"github.com/podplane/podplane/internal/oidccreate"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

// newOIDCCreateCmd builds the OIDC create command and wires config collection,
// Terraform generation, and optional apply.
func newOIDCCreateCmd(c *config.Config) *cobra.Command {
	var cfgPath string
	var noApply bool
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate OIDC configuration and deploy infrastructure",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgPath == "" {
				cfgPath = defaultOIDCConfigName
			}
			path, err := filepath.Abs(cfgPath)
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); os.IsNotExist(err) {
				originDir, err := os.Getwd()
				if err != nil {
					return err
				}
				path, err = infrafiles.ConfirmConfigPath(cmd.Context(), path, originDir, "OIDC config and OpenTofu/Terraform", "my-oidc-server")
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			_, err = oidccreate.Run(cmd.Context(), oidccreate.Options{
				ConfigPath:  path,
				NoApply:     noApply,
				AutoApprove: autoApprove,
			})
			return err
		},
	}
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		parent := cmd.Context()
		defer cmd.SetContext(parent)
		return tui.RunFlow(parent, "Podplane · Create OIDC server", func(ctx context.Context) error {
			cmd.SetContext(ctx)
			return run(cmd, args)
		})
	}
	cmd.Flags().StringVarP(&cfgPath, "oidc-config", "f", defaultOIDCConfigName, "Path to the OIDC config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Generate OpenTofu/Terraform files but do not run apply")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and approve the saved infrastructure plan")
	return cmd
}
