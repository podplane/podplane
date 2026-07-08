// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/podplane/podplane/internal/buildvars"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/execwrap"
	"github.com/spf13/cobra"
)

var (
	flagVerbose bool
	flagVersion bool
)

var rootCmd = &cobra.Command{
	Use:   "podplane",
	Short: "Podplane CLI",
	Long:  `podplane is the official CLI of the Podplane Kubernetes distribution & PaaS <https://podplane.dev>`,
}

func init() {
	cobra.EnableCommandSorting = false
	cobra.EnableTraverseRunHooks = true
	pflags := rootCmd.PersistentFlags()
	pflags.BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose output")
	pflags.Lookup("verbose").NoOptDefVal = "true"
	pflags.BoolVar(&flagVersion, "version", false, "Show version information")
}

func NewRootCmd(c *config.Config) *cobra.Command {
	// Create logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
	}))

	// Suppress usage output once Cobra has accepted args and flags.
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	}

	// Define root command
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		// Determine if we should be silent (hooks commands need clean stdout)
		silentMode := (cmd.HasParent() && cmd.Parent().Name() == "hooks") || cmd.Name() == "hooks"

		// Apply log level filtering based on silent and verbose settings
		filteredLogger := logger
		if silentMode {
			filteredLogger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
				Level: slog.LevelError,
			}))
		} else if flagVerbose {
			filteredLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				AddSource: true,
				Level:     slog.LevelDebug,
			}))
		}
		slog.SetDefault(filteredLogger)

		// Check for version flag
		if flagVersion {
			fmt.Printf("podplane CLI %s\n", buildvars.BuildVersion())
			if flagVerbose {
				fmt.Printf("build version: %s\n", buildvars.BuildVersion())
				fmt.Printf("build date: %s\n", buildvars.BuildDate())
				fmt.Printf("commit hash: %s\n", buildvars.CommitHash())
				fmt.Printf("commit date: %s\n", buildvars.CommitDate())
				fmt.Printf("commit branch: %s\n", buildvars.CommitBranch())
			}
			return
		}

		// Log modes
		if flagVerbose {
			filteredLogger.Debug("Verbose ouput ENABLED")
		}

		// Check global runtime dependencies
		if err := execwrap.Installed([]string{"kubectl"}); err != nil {
			fmt.Println("Error checking for runtime dependencies:", err)
			os.Exit(1)
		}

		// If no args/subcommand, show help.
		_ = cmd.Help()
	}

	// Add command groups
	rootCmd.AddGroup(
		&cobra.Group{ID: "auth", Title: "Authentication:"},
		&cobra.Group{ID: "dev", Title: "Development:"},
		&cobra.Group{ID: "local", Title: "Local Clusters:"},
		&cobra.Group{ID: "infra", Title: "Infrastructure:"},
	)

	// Add subcommands to Auth group
	loginCmd := newLoginCmd(c)
	loginCmd.GroupID = "auth"
	rootCmd.AddCommand(loginCmd)
	logoutCmd := newLogoutCmd(c)
	logoutCmd.GroupID = "auth"
	rootCmd.AddCommand(logoutCmd)

	// Add subcommands to Dev group
	buildCmd := newBuildCmd(c)
	buildCmd.GroupID = "dev"
	rootCmd.AddCommand(buildCmd)
	pushCmd := newPushCmd(c)
	pushCmd.GroupID = "dev"
	rootCmd.AddCommand(pushCmd)
	deployCmd := newDeployCmd(c)
	deployCmd.GroupID = "dev"
	rootCmd.AddCommand(deployCmd)
	secretCmd := newSecretCmd(c)
	secretCmd.GroupID = "dev"
	rootCmd.AddCommand(secretCmd)
	removeCmd := newRemoveCmd(c)
	removeCmd.GroupID = "dev"
	rootCmd.AddCommand(removeCmd)
	logsCmd := newLogsCmd(c)
	logsCmd.GroupID = "dev"
	rootCmd.AddCommand(logsCmd)

	// Add subcommands to Local group
	localCmd := newLocalCmd(c)
	localCmd.GroupID = "local"
	rootCmd.AddCommand(localCmd)
	depsCmd := newDepsCmd(c)
	depsCmd.GroupID = "local"
	rootCmd.AddCommand(depsCmd)

	// Add subcommands to Infra group
	oidcCmd := newOIDCCmd(c)
	oidcCmd.GroupID = "infra"
	rootCmd.AddCommand(oidcCmd)
	clusterCmd := newClusterCmd(c)
	clusterCmd.GroupID = "infra"
	rootCmd.AddCommand(clusterCmd)
	installCmd := newInstallCmd(c)
	installCmd.GroupID = "infra"
	rootCmd.AddCommand(installCmd)
	uninstallCmd := newUninstallCmd(c)
	uninstallCmd.GroupID = "infra"
	rootCmd.AddCommand(uninstallCmd)

	// Add ungrouped subcommands
	hooksCmd := newHooksCmd(c)
	hooksCmd.Hidden = true
	rootCmd.AddCommand(hooksCmd)
	rootCmd.AddCommand(newVersionCmd())

	return rootCmd
}
