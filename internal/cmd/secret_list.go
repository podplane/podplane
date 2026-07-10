// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/podplane/podplane/internal/config"
	secretapi "github.com/podplane/podplane/internal/secrets"
	"github.com/spf13/cobra"
)

// newSecretListCmd builds the secret list command.
func newSecretListCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secret provider keys without showing their values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveSecretContext(c)
			if err != nil {
				return err
			}
			client := secretapi.Client{Context: secretFlags.Context, Kubeconfig: secretFlags.Kubeconfig}
			keyspace, err := client.Get(ctx.Namespace, ctx.KeyspaceName)
			if err != nil {
				return err
			}
			if secretFlags.HideArchived {
				entries := keyspace.Status.Entries[:0]
				for _, entry := range keyspace.Status.Entries {
					if entry.Status != "archived" {
						entries = append(entries, entry)
					}
				}
				keyspace.Status.Entries = entries
			}
			if secretFlags.Output != "" {
				return printKeyspace(keyspace, secretFlags.Output)
			}
			if len(keyspace.Status.Entries) == 0 {
				cmd.Printf("No secrets found for %q in namespace %q using provider %q\n", secretFlags.For, ctx.Namespace, ctx.Provider)
				return nil
			}
			for _, entry := range keyspace.Status.Entries {
				fmt.Printf("%s\t%s\t%s\n", entry.Key, entry.Status, entry.BackendPath)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&secretFlags.HideArchived, "hide-archived", false, "Hide archived/restorable keys")
	cmd.Flags().StringVarP(&secretFlags.Output, "output", "o", "", "Output format: json or yaml")
	return cmd
}
