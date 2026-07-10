// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/secrets"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

// newSecretDeleteCmd builds the secret delete command.
func newSecretDeleteCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [KEY]",
		Short: "Archive or destroy secrets provider secrets via Podplane operator",
		Args: func(cmd *cobra.Command, args []string) error {
			if secretFlags.All {
				if len(args) != 0 {
					return fmt.Errorf("delete --all does not accept KEY")
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("delete requires KEY unless --all is set")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveSecretContext(c)
			if err != nil {
				return err
			}
			key := ""
			if len(args) == 1 {
				key = args[0]
				if err := secrets.ValidateKey(key); err != nil {
					return err
				}
			}
			if secretFlags.Destroy && !secretFlags.AutoApprove {
				target := key
				if secretFlags.All {
					target = "all keys under this SecretProviderClass boundary"
				}
				ok, err := tui.Confirm("Permanently destroy "+target+"?", secretFlags.AutoApprove, 0)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("destroy cancelled")
				}
			}
			client := secrets.Client{Context: secretFlags.Context, Kubeconfig: secretFlags.Kubeconfig}
			if err := client.Delete(ctx.Namespace, ctx.KeyspaceName, key, secretFlags.Destroy); err != nil {
				return err
			}
			action := "Archived"
			if secretFlags.Destroy {
				action = "Destroyed"
			}
			if key == "" {
				cmd.Printf("%s all secrets for %q in namespace %q using provider %q\n", action, secretFlags.For, ctx.Namespace, ctx.Provider)
				return nil
			}
			cmd.Printf("%s secret %q for %q in namespace %q using provider %q\n", action, key, secretFlags.For, ctx.Namespace, ctx.Provider)
			return nil
		},
	}
	cmd.Flags().BoolVar(&secretFlags.All, "all", false, "Delete every key under the selected SecretProviderClass boundary")
	cmd.Flags().BoolVar(&secretFlags.Destroy, "destroy", false, "Permanently destroy instead of archive")
	cmd.Flags().BoolVarP(&secretFlags.AutoApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	return cmd
}
