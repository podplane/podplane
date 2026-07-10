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
	installContext     string
	installKubeconfig  string
	installAutoApprove bool
)

func newInstallCmd(_ *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <component>",
		Short: "Install an addon component into the cluster",
		Long: `Install an addon component into the cluster with an opinionated, tested
configuration. The CLI patches the platform-components HelmRelease so Flux
CD reconciles the component (and any of its dependencies that are not yet
enabled).

You must already be authenticated to the cluster - run ` + "`podplane login`" + `
first if you have not yet logged in.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			name := args[0]

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cfg, err := components.Read(ctx, installContext, installKubeconfig)
			if err != nil {
				return err
			}
			if !cfg.Has(name) {
				return fmt.Errorf("unknown component %q: the platform-components HelmRelease does not define a component with that name", name)
			}

			plan, err := cfg.ResolveEnable(name)
			if err != nil {
				return err
			}
			if plan.IsEmpty() {
				fmt.Printf("Component %q is already installed.\n", name)
				return nil
			}

			extra := make([]string, 0, len(plan.Apps)+len(plan.CRDs))
			for _, n := range plan.Apps {
				if n != name {
					extra = append(extra, n)
				}
			}
			for _, n := range plan.CRDs {
				if n != name {
					extra = append(extra, n)
				}
			}
			if len(extra) > 0 {
				fmt.Printf("Installing %q will also enable the following dependencies:\n  %s\n", name, strings.Join(extra, ", "))
				ok, err := tui.Confirm("Continue?", installAutoApprove, 0)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("install cancelled")
				}
			}

			if err := components.SetEnabled(ctx, installContext, installKubeconfig, plan.Apps, plan.CRDs, true); err != nil {
				return err
			}

			items := cfg.HelmReleaseRefs(plan)
			required := make([]tui.StatusProgressItem, 0, len(items))
			for _, item := range items {
				required = append(required, componentStatusProgressItem(item))
			}
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer waitCancel()
			if err := runComponentStatusProgress(waitCtx, "Installing component dependencies", installContext, installKubeconfig, items, required); err != nil {
				return err
			}

			fmt.Printf("✓ Installed %q.\n", name)
			if entry, isApp, _ := cfg.Get(name); isApp && entry.Namespace != "" {
				fmt.Printf("   Watch progress with: kubectl -n %s get all\n", entry.Namespace)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&installContext, "context", "", "kubeconfig context to use (default: current kubeconfig context)")
	cmd.Flags().StringVar(&installKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().BoolVarP(&installAutoApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	return cmd
}
