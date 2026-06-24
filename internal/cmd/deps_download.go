// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

var depsDownloadCmd = &cobra.Command{
	Use:          "download",
	Short:        "Downloads the latest dependency files",
	Long:         `Download fetches the latest dependency files. Generally you should not be running this directly.`,
	SilenceUsage: true,
}

var (
	depsDownloadDryRun             bool
	depsDownloadVMConfigManifest   string
	depsDownloadComponentsManifest string
	depsDownloadTemplatesManifest  string
	depsDownloadSeedsManifest      string
	depsDownloadArchs              string
	depsDownloadProviders          string
	depsDownloadAddons             string
	depsDownloadClusterConfig      string
	depsDownloadSkipSeeds          bool
	depsDownloadSkipComponentsGit  bool
)

func init() {
	depsDownloadCmd.Flags().BoolVar(&depsDownloadDryRun, "dry-run", false,
		"Print what would be downloaded without actually downloading anything")
	depsDownloadCmd.Flags().StringVar(&depsDownloadVMConfigManifest, "vmconfig", "",
		"Path to a local vmconfig manifest JSON file to use instead of downloading the manifest")
	depsDownloadCmd.Flags().StringVar(&depsDownloadComponentsManifest, "components", "",
		"Path to a local components manifest JSON file to use instead of downloading the manifest")
	depsDownloadCmd.Flags().StringVar(&depsDownloadTemplatesManifest, "templates", "",
		"Path to a local templates manifest JSON file to use instead of downloading the manifest")
	depsDownloadCmd.Flags().StringVar(&depsDownloadSeedsManifest, "seeds", "",
		"Path to a local seeds manifest JSON file to use instead of downloading the manifest")
	depsDownloadCmd.Flags().StringVar(&depsDownloadArchs, "arch", "",
		"Comma-separated target architectures to download (amd64,arm64). Defaults to the configured architecture")
	depsDownloadCmd.Flags().StringVar(&depsDownloadProviders, "providers", "",
		"Comma-separated provider-specific dependencies and component images to include (for example aws,google,proxmox), or all")
	depsDownloadCmd.Flags().StringVar(&depsDownloadAddons, "addons", "",
		"Comma-separated addon component images to include in addition to the recommended addons, or all")
	depsDownloadCmd.Flags().StringVarP(&depsDownloadClusterConfig, "cluster-config", "f", "",
		"Path to a cluster config file to infer providers")
	depsDownloadCmd.Flags().BoolVar(&depsDownloadSkipSeeds, "skip-seeds", false,
		"Skip downloading seed manifests and snapshots")
	depsDownloadCmd.Flags().BoolVar(&depsDownloadSkipComponentsGit, "skip-components-git", false,
		"Skip cloning or fetching the Git source declared by the components manifest")
}

func newDepsDownloadCmd(manager *deps.Manager, kind, arch string) *cobra.Command {
	depsDownloadCmd.RunE = func(cmd *cobra.Command, args []string) error {
		archs := []string{arch}
		if depsDownloadArchs != "" {
			archs = archs[:0]
			for _, part := range strings.Split(depsDownloadArchs, ",") {
				if arch := strings.TrimSpace(part); arch != "" {
					archs = append(archs, arch)
				}
			}
		}
		providers := []string{depsDownloadProviders}
		addons := components.RecommendedAddons()
		addons = append(addons, depsDownloadAddons)
		if depsDownloadClusterConfig != "" {
			cfg, err := clusterconfig.Load(depsDownloadClusterConfig)
			if err != nil {
				return err
			}
			for _, provider := range cfg.Cluster.Providers {
				providers = append(providers, provider.Kind)
			}
			for _, domain := range cfg.Cluster.Domains {
				providers = append(providers, domain.Provider.Kind)
			}
			providers = append(providers, inferredSecretsProviders(providers)...)
		}

		if depsDownloadDryRun {
			fmt.Println("Fetching latest dependency manifest...")
			for i, arch := range archs {
				err := manager.Download(kind, arch, deps.DownloadOptions{
					DryRun:                    true,
					Archs:                     archs,
					Providers:                 providers,
					Addons:                    addons,
					SkipCrossArchDependencies: i > 0,
					VMConfigManifestPath:      depsDownloadVMConfigManifest,
					ComponentsManifestPath:    depsDownloadComponentsManifest,
					TemplatesManifestPath:     depsDownloadTemplatesManifest,
					SeedsManifestPath:         depsDownloadSeedsManifest,
					SkipSeeds:                 depsDownloadSkipSeeds,
					SkipComponentsGit:         depsDownloadSkipComponentsGit,
				})
				if err != nil {
					fmt.Printf("Error downloading the latest dependency files: %v\n", err)
					return err
				}
			}
			return nil
		}

		err := tui.RunDownloadProgress("Downloading Podplane dependencies", func(progress func(deps.DownloadEvent)) error {
			for i, arch := range archs {
				err := manager.Download(kind, arch, deps.DownloadOptions{
					Archs:                     archs,
					Providers:                 providers,
					Addons:                    addons,
					Progress:                  progress,
					SkipCrossArchDependencies: i > 0,
					VMConfigManifestPath:      depsDownloadVMConfigManifest,
					ComponentsManifestPath:    depsDownloadComponentsManifest,
					TemplatesManifestPath:     depsDownloadTemplatesManifest,
					SeedsManifestPath:         depsDownloadSeedsManifest,
					SkipSeeds:                 depsDownloadSkipSeeds,
					SkipComponentsGit:         depsDownloadSkipComponentsGit,
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			fmt.Printf("Error downloading the latest dependency files: %v\n", err)
		}
		return err
	}

	return depsDownloadCmd
}

// inferredSecretsProviders returns secrets provider component selectors implied by infra provider selectors.
func inferredSecretsProviders(providers []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range providers {
		for _, provider := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(provider)) {
			case "aws":
				if !seen["aws"] {
					seen["aws"] = true
					out = append(out, "aws")
				}
			case "gcp", "google":
				if !seen["gcp"] {
					seen["gcp"] = true
					out = append(out, "gcp")
				}
			case "proxmox":
				if !seen["openbao"] {
					seen["openbao"] = true
					out = append(out, "openbao")
				}
			}
		}
	}
	return out
}
