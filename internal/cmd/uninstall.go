// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

var (
	uninstallContext     string
	uninstallKubeconfig  string
	uninstallAutoApprove bool
)

func newUninstallCmd(_ *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall <component>",
		Short: "Remove an addon component from the cluster",
		Long: `Remove a previously installed addon component from the cluster. The CLI
patches the platform-components HelmRelease so Flux CD removes the
component's HelmRelease.

Core components and CRDs cannot be uninstalled. Components that other
enabled components depend on cannot be uninstalled until their dependents
are removed first.

You must already be authenticated to the cluster - run ` + "`podplane login`" + `
first if you have not yet logged in.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			name := args[0]

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cfg, err := components.Read(ctx, uninstallContext, uninstallKubeconfig)
			if err != nil {
				return err
			}
			if !cfg.Has(name) {
				return fmt.Errorf("unknown component %q: the platform-components HelmRelease does not define a component with that name", name)
			}
			entry, isApp, _ := cfg.Get(name)
			if !isApp {
				return fmt.Errorf("component %q is a CRD chart; CRDs are not uninstalled via `podplane uninstall` to avoid deleting custom resource data. To remove the chart manually, set its `enabled` field to false in the platform-components HelmRelease values", name)
			}
			if entry.Core {
				return fmt.Errorf("component %q is a core component and cannot be uninstalled", name)
			}
			if !entry.Enabled {
				fmt.Printf("Component %q is not installed.\n", name)
				return nil
			}
			if dependents := cfg.EnabledDependents(name); len(dependents) > 0 {
				return fmt.Errorf("cannot uninstall %q because it is required by: %s. Uninstall those first", name, strings.Join(dependents, ", "))
			}

			ok, err := tui.Confirm(fmt.Sprintf("Uninstall component %q?", name), uninstallAutoApprove, 0)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("uninstall cancelled")
			}

			if err := components.SetEnabled(ctx, uninstallContext, uninstallKubeconfig, []string{name}, nil, false); err != nil {
				return err
			}

			fmt.Printf("✓ Patched platform-components to uninstall %q. Flux CD is removing the component.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&uninstallContext, "context", "", "kubeconfig context to use (default: current kubeconfig context)")
	cmd.Flags().StringVar(&uninstallKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().BoolVarP(&uninstallAutoApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	return cmd
}
