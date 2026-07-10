// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deploy"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var logsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Tail logs for a deployed app",
	Example: "  # Tail logs for the hello app\n" +
		"  podplane logs hello\n\n" +
		"  # Tail logs for every container with prefixed lines\n" +
		"  podplane logs hello --all\n\n" +
		"  # Tail logs for a specific container\n" +
		"  podplane logs hello --container hello-caddy",
	Args: cobra.ExactArgs(1),
}

var (
	logsNamespace  string
	logsContext    string
	logsKubeconfig string
	logsContainer  string
	logsAll        bool
)

func init() {
	logsCmd.Flags().StringVarP(&logsNamespace, "namespace", "n", "", "Kubernetes namespace the app was deployed into")
	logsCmd.Flags().StringVar(&logsContext, "context", "", "The name of the kubeconfig context to use")
	logsCmd.Flags().StringVar(&logsKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	logsCmd.Flags().StringVarP(&logsContainer, "container", "c", "", "Container to tail logs from")
	logsCmd.Flags().BoolVar(&logsAll, "all", false, "Tail logs from all containers with prefixed lines")
}

func newLogsCmd(c *config.Config) *cobra.Command {
	_ = c
	logsCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if logsAll && logsContainer != "" {
			return fmt.Errorf("--all and --container cannot be used together")
		}
		var selectContainer deploy.SelectContainerFunc
		if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
			selectContainer = func(containers []string) (string, bool, error) {
				return tui.SelectString("Tail logs", "Choose a container", containers)
			}
		}
		return deploy.Logs(deploy.LogsOptions{
			Name:            args[0],
			Namespace:       logsNamespace,
			Context:         logsContext,
			Kubeconfig:      logsKubeconfig,
			Container:       logsContainer,
			AllContainers:   logsAll,
			SelectContainer: selectContainer,
		})
	}
	return logsCmd
}
