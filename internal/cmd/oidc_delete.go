// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/oidcconfig"
	"github.com/podplane/podplane/internal/oidcdelete"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

// newOIDCDeleteCmd builds the OIDC delete command and delegates destroy
// workflow behavior to the oidcdelete package.
func newOIDCDeleteCmd(c *config.Config) *cobra.Command {
	var cfgPath string
	var noApply bool
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Destroy deployed OIDC infrastructure",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgPath == "" {
				cfgPath = defaultOIDCConfigName
			}
			path, err := filepath.Abs(cfgPath)
			if err != nil {
				return err
			}
			if _, err := oidcconfig.Load(path); err != nil {
				return err
			}
			if noApply {
				fmt.Println("--no-apply set; OIDC config validated and no destroy was run.")
				return nil
			}
			dir := filepath.Dir(path)
			executor, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			if err := oidcdelete.Delete(context.Background(), oidcdelete.DeleteOptions{
				StackDir:    dir,
				Executor:    executor,
				AutoApprove: autoApprove,
				Confirm: func(message string) (bool, error) {
					return tui.Confirm(message, autoApprove, 0)
				},
			}); err != nil {
				return err
			}
			fmt.Println("OIDC destroy completed. Config and generated .tf files were left in place.")
			return nil
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "oidc-config", "f", defaultOIDCConfigName, "Path to the OIDC config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Validate config but do not run destroy")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform")
	return cmd
}
