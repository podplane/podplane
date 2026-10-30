// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/podplane/podplane/internal/bundle"
	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/tui"
)

func newDepsBundleCmd(manager *deps.Manager, kind, cacheDir string) *cobra.Command {
	opts := bundle.Options{CacheDir: cacheDir}
	var providers, clusterConfig string
	cmd := &cobra.Command{
		Use: "bundle <directory>", Short: "Create self-contained offline installation bundle archives",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := depsDownloadProviderList(providers, clusterConfig)
			if err != nil {
				return err
			}
			opts.Providers = selected
			var existing []string
			for _, archive := range bundle.ArchivePaths(args[0], opts.Platforms) {
				if _, err := os.Lstat(archive); err == nil {
					existing = append(existing, archive)
				}
			}
			if len(existing) > 0 {
				ok, err := tui.Confirm("Delete existing bundle archives?\n"+strings.Join(existing, "\n"), false, 1)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("bundle cancelled; existing archives were kept")
				}
				for _, archive := range existing {
					if err := os.Remove(archive); err != nil {
						return err
					}
				}
			}
			var stage string
			if err := tui.RunDownloadProgress("Preparing offline Podplane bundles", func(progress func(deps.DownloadEvent)) error {
				selected := opts
				selected.Addons = append(components.AgenticAddons(), opts.Addons...)
				selected.Progress = progress
				stage, err = bundle.Create(cmd.Context(), manager, args[0], kind, selected)
				return err
			}); err != nil {
				if stage != "" {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Staging path: %s. Rerun without --refresh to resume.\n", stage)
				}
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Offline bundles are ready in %s. Copy the podplane-<platform>.tar.gz matching each laptop.\n", args[0])
			if slices.Contains(opts.Platforms, "macos-amd64") {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Intel Mac bundles do not include QEMU. Recipients must preinstall QEMU (qemu-img and qemu-system-x86_64 on PATH) before going offline.")
			}
			if !opts.Cleanup {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Staging retained at %s for more copies. Use --cleanup to remove it after a successful copy, or --refresh to prepare newer versions.\n", stage)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&opts.Platforms, "platforms", []string{"macos-arm64", "macos-amd64", "linux-arm64", "linux-amd64"}, "Host platforms to include (Intel macOS requires preinstalled QEMU)")
	cmd.Flags().StringVar(&opts.Version, "version", "", "Podplane CLI release tag (default latest stable)")
	cmd.Flags().BoolVar(&opts.Refresh, "refresh", false, "Rebuild staging with current releases, reusing cached downloads")
	cmd.Flags().BoolVar(&opts.Cleanup, "cleanup", false, "Remove staging after successful archiving; retain downloaded artifacts")
	cmd.Flags().StringVar(&providers, "providers", "", "Provider selection, as for deps download (default openbao,local)")
	cmd.Flags().StringSliceVar(&opts.Addons, "addons", nil, "Additional addons, or all (agentic addons are always included)")
	cmd.Flags().StringVarP(&clusterConfig, "cluster-config", "f", "", "Infer providers from a cluster config")
	return cmd
}
