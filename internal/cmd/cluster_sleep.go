// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/podplane/podplane/internal/clustersleep"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/tui"
)

// newClusterSleepCmd wires context selection and forced-sleep confirmation to a nonblocking sleep request.
func newClusterSleepCmd(_ *config.Config) *cobra.Command {
	var kubeContext, kubeconfig string
	var force, autoApprove bool
	cmd := &cobra.Command{
		Use:   "sleep",
		Short: "Request cluster sleep and return immediately",
		Long: `Request sleep by annotating the Nstance-managed CAPI Cluster.

Normal sleep applies the operator's sleep guards.

Forced sleep can interrupt active workloads and connections and requires
confirmation (or -y in noninteractive use).

The context defaults to the current kubeconfig context. Use --context to select
a different cluster. The request is submitted without waiting for sleep to
complete. There is no wake command - any Kubernetes API server request or ingress
request will trigger a wake so use podplane or kubectl or curl a cluster domain.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			err := clustersleep.Request(cmd.Context(), clustersleep.Options{
				Context: kubeContext, Kubeconfig: kubeconfig,
				Force: force, AutoApprove: autoApprove,
				Warn: func(message string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning:", message) },
				Confirm: func(message string) (bool, error) {
					if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
						return false, fmt.Errorf("noninteractive forced sleep requires --auto-approve (-y)")
					}
					return tui.Confirm(message, false, 1)
				},
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Sleep requested; not waiting for completion.")
			return nil
		},
	}
	cmd.Flags().StringVar(&kubeContext, "context", "", "The name of the kubeconfig context to use")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().BoolVar(&force, "force", false, "Bypass sleep guards; may interrupt active workloads and connections")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Explicitly approve forced sleep without prompting")
	return cmd
}
