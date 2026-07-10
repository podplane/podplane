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

var shellCmd = &cobra.Command{
	Use:   "shell <name> [-- command...]",
	Short: "Open a shell or run a command in a deployed app",
	Example: "  # Open a shell in the hello app\n" +
		"  podplane shell hello\n\n" +
		"  # Run one command in the hello app\n" +
		"  podplane shell hello -- npm run migrate\n\n" +
		"  # Shell into a specific container\n" +
		"  podplane shell hello --container web",
	Args: cobra.MinimumNArgs(1),
}

var (
	shellNamespace  string
	shellContext    string
	shellKubeconfig string
	shellContainer  string
	shellDebugImage string
	shellDebug      bool
	shellNoDebug    bool
)

// init registers shell command flags.
func init() {
	shellCmd.Flags().StringVarP(&shellNamespace, "namespace", "n", "", "Kubernetes namespace the app was deployed into")
	shellCmd.Flags().StringVar(&shellContext, "context", "", "The name of the kubeconfig context to use")
	shellCmd.Flags().StringVar(&shellKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	shellCmd.Flags().StringVarP(&shellContainer, "container", "c", "", "Container to shell into")
	shellCmd.Flags().BoolVar(&shellDebug, "debug", false, "Start an ephemeral debug container without prompting when no shell is available")
	shellCmd.Flags().BoolVar(&shellNoDebug, "no-debug", false, "Skip ephemeral debug container fallback")
	shellCmd.Flags().StringVar(&shellDebugImage, "debug-image", "busybox:latest", "Image to use for ephemeral debug containers")
}

// newShellCmd creates the app shell command.
func newShellCmd(c *config.Config) *cobra.Command {
	_ = c
	shellCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if shellDebug && shellNoDebug {
			return fmt.Errorf("--debug and --no-debug cannot be used together")
		}
		if len(args) > 1 && (shellDebug || shellNoDebug || cmd.Flags().Changed("debug-image")) {
			return fmt.Errorf("--debug, --no-debug, and --debug-image only apply to interactive shell fallback, not `-- <command...>`")
		}
		var selectContainer deploy.SelectContainerFunc
		interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
		if interactive {
			selectContainer = func(containers []string) (string, bool, error) {
				return tui.SelectString("Open shell", "Choose a container", containers)
			}
		}
		var confirmDebug deploy.ConfirmDebugFunc
		if shellDebug {
			confirmDebug = func(string) (bool, error) { return true, nil }
		} else if interactive && len(args) == 1 && !shellNoDebug {
			confirmDebug = func(message string) (bool, error) {
				return tui.Confirm(message, false, 0)
			}
		}
		var confirmRestart deploy.ConfirmRestartFunc
		if interactive && len(args) == 1 {
			confirmRestart = func(message string) (bool, error) {
				return tui.Confirm(message, false, 1)
			}
		}
		return deploy.Shell(deploy.ShellOptions{
			Name:            args[0],
			Namespace:       shellNamespace,
			Context:         shellContext,
			Kubeconfig:      shellKubeconfig,
			Container:       shellContainer,
			Command:         args[1:],
			SelectContainer: selectContainer,
			ShellPrompt:     interactive,
			NoDebug:         shellNoDebug,
			DebugImage:      shellDebugImage,
			ConfirmDebug:    confirmDebug,
			ConfirmRestart:  confirmRestart,
		})
	}
	return shellCmd
}
