// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/secrets"
	"github.com/spf13/cobra"
)

// newSecretRestoreCmd builds the secret restore command.
func newSecretRestoreCmd(c *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "restore KEY",
		Short: "Restore an archived secret provider secret key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := secrets.ValidateKey(args[0]); err != nil {
				return err
			}
			ctx, err := resolveSecretContext(c)
			if err != nil {
				return err
			}
			client := secrets.Client{Context: secretFlags.Context, Kubeconfig: secretFlags.Kubeconfig}
			if _, err := client.Put(secrets.NewKeyspaceRequest(ctx.Namespace, ctx.KeyspaceName, args[0], "restore", nil)); err != nil {
				return err
			}
			cmd.Printf("Restored secret %q for %q in namespace %q using provider %q\n", args[0], secretFlags.For, ctx.Namespace, ctx.Provider)
			return nil
		},
	}
}
