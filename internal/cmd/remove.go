// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deploy"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a previously deployed app",
	Example: "  # Remove the hello app\n" +
		"  podplane remove --name hello",
	Args: cobra.NoArgs,
}

var (
	removeName       string
	removeNamespace  string
	removeContext    string
	removeKubeconfig string
)

// init registers app removal flags and requires a deployment name.
func init() {
	removeCmd.Flags().StringVar(&removeName, "name", "", "Name of the app deployment to remove")
	removeCmd.Flags().StringVarP(&removeNamespace, "namespace", "n", "", "Kubernetes namespace the app was deployed into")
	removeCmd.Flags().StringVar(&removeContext, "context", "", "The name of the kubeconfig context to use")
	removeCmd.Flags().StringVar(&removeKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	_ = removeCmd.MarkFlagRequired("name")
}

// newRemoveCmd wires app removal using the selected Kubernetes namespace and context.
func newRemoveCmd(c *config.Config) *cobra.Command {
	_ = c
	removeCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return deploy.Remove(deploy.RemoveOptions{
			Name:       removeName,
			Namespace:  removeNamespace,
			Context:    removeContext,
			Kubeconfig: removeKubeconfig,
		})
	}
	return removeCmd
}
