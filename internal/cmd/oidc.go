// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/podplane/podplane/internal/config"
	"github.com/spf13/cobra"
)

var oidcCmd = &cobra.Command{
	Use:   "oidc",
	Short: "Manage OIDC server infrastructure",
	Long:  "Commands to generate OpenTofu/Terraform files and manage Podplane OIDC server infrastructure.",
}

const defaultOIDCConfigName = "podplane.oidc.jsonc"

// newOIDCCmd groups OIDC infrastructure creation and deletion commands with fallback help.
func newOIDCCmd(c *config.Config) *cobra.Command {
	oidcCmd.Run = func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	}
	oidcCmd.AddCommand(newOIDCCreateCmd(c))
	oidcCmd.AddCommand(newOIDCDeleteCmd(c))
	return oidcCmd
}
