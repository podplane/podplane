// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/secrets"
	"github.com/spf13/cobra"
)

// newSecretCreateCmd builds the secret create command.
func newSecretCreateCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create --for APP_NAME KEY",
		Short: "Create a secret provider key/value",
		Example: "  # Create a secret for the app deployed with `podplane deploy --name hello`\n" +
			"  podplane secret create --for hello secure-message\n\n" +
			"  # Mount it into the same app at /var/run/podplane/secrets/secure-message\n" +
			"  podplane deploy web --name hello --secret secure-message",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if secretFlags.DryRun != "" && secretFlags.DryRun != "client" {
				return fmt.Errorf("--dry-run must be client when set")
			}
			if err := secrets.ValidateKey(args[0]); err != nil {
				return err
			}
			ctx, err := resolveSecretContext(c)
			if err != nil {
				return err
			}
			value, err := readSecretValue("create", args[0], secretFlags.Stdin, secretFlags.File)
			if err != nil {
				return err
			}
			client := secrets.Client{Context: secretFlags.Context, Kubeconfig: secretFlags.Kubeconfig}
			opts := secrets.WriteOptions{Namespace: ctx.Namespace, KeyspaceName: ctx.KeyspaceName, ClusterID: ctx.ClusterID, Key: args[0], Operation: "create", Value: value}
			request, err := client.EncryptedRequest(opts)
			if err != nil {
				return err
			}
			if secretFlags.DryRun == "client" {
				return printKeyspace(request, secretFlags.Output)
			}
			response, err := client.PutEncrypted(request, opts)
			if err != nil {
				return err
			}
			if secretFlags.Output != "" {
				return printKeyspace(response, secretFlags.Output)
			}
			fmt.Printf("Created secret %q for %q in namespace %q using provider %q\n", args[0], secretFlags.For, ctx.Namespace, ctx.Provider)
			return nil
		},
	}
	cmd.Flags().BoolVar(&secretFlags.Stdin, "stdin", false, "Read the secret value from stdin")
	cmd.Flags().StringVar(&secretFlags.File, "file", "", "Read the secret value from a file")
	cmd.Flags().StringVar(&secretFlags.DryRun, "dry-run", "", "If set to client, print the encrypted SecretProviderKeyspace manifest without writing")
	cmd.Flags().StringVarP(&secretFlags.Output, "output", "o", "", "Output format for dry-run/list responses: json or yaml")
	return cmd
}
