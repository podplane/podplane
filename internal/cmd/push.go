// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/registryclient"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

var (
	pushContext    string
	pushKubeconfig string
	pushDocker     string
	pushApprove    bool
)

// newPushCmd creates the `podplane push` command.
func newPushCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <local-image> [<remote-image>]",
		Short: "Push a local image to the cluster registry",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			docker := normalizeDockerFlag(pushDocker)
			remote := ""
			if len(args) > 1 {
				remote = args[1]
			}
			ref, err := registryclient.Push(cmd.Context(), registryclient.Options{
				Config:     c,
				Source:     args[0],
				Remote:     remote,
				StoreRoot:  deps.NewManager(c.DepsBaseURL(), c.DepsCacheDir()).RegistryCacheDir(),
				Docker:     docker,
				Context:    pushContext,
				Kubeconfig: pushKubeconfig,
				Stderr:     os.Stderr,
				Confirm: func(message string) (bool, error) {
					return tui.Confirm(message, pushApprove)
				},
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), ref)
			return err
		},
	}
	cmd.Flags().StringVar(&pushContext, "context", "", "kubeconfig context to use (default: current kubeconfig context)")
	cmd.Flags().StringVar(&pushKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().StringVar(&pushDocker, "docker", "docker", "use Docker as a fallback source, optionally with a Docker binary path; use --docker=false to disable")
	cmd.Flags().Lookup("docker").NoOptDefVal = "docker"
	cmd.Flags().BoolVarP(&pushApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	return cmd
}
