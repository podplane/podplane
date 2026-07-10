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

// newSecretDestroyCmd builds the secret destroy command.
func newSecretDestroyCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy KEY",
		Short: "Permanently destroy a secrets provider secret via Podplane operator",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := secrets.ValidateKey(args[0]); err != nil {
				return err
			}
			ctx, err := resolveSecretContext(c)
			if err != nil {
				return err
			}
			if !secretFlags.AutoApprove {
				ok, err := tui.Confirm("Permanently destroy "+args[0]+"?", secretFlags.AutoApprove)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("destroy cancelled")
				}
			}
			client := secrets.Client{Context: secretFlags.Context, Kubeconfig: secretFlags.Kubeconfig}
			if err := client.Delete(ctx.Namespace, ctx.KeyspaceName, args[0], true); err != nil {
				return err
			}
			cmd.Printf("Destroyed secret %q for %q in namespace %q using provider %q\n", args[0], secretFlags.For, ctx.Namespace, ctx.Provider)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&secretFlags.AutoApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	return cmd
}
