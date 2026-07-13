// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clustercreate"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/infrafiles"
	"github.com/podplane/podplane/internal/oidccreate"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tfgen"
	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/pkg/seeds"
	"github.com/spf13/cobra"
)

// newClusterCreateCmd builds the cluster create command and wires config
// collection, Terraform generation, and optional apply.
func newClusterCreateCmd(c *config.Config) *cobra.Command {
	var cfgPath string
	var noApply bool
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Generate cluster configuration and deploy infrastructure",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgPath == "" {
				cfgPath = defaultClusterConfigName
			}
			path, err := filepath.Abs(cfgPath)
			if err != nil {
				return err
			}

			// Prepare dependency cache access used while creating or generating
			// cluster infrastructure files.
			depsManager := deps.NewManager(c.DepsBaseURL(), c.DepsCacheDir())

			// Load an existing cluster config, or create one first so the rest of
			// the command can operate on a single resolved config value.
			var cfg *clusterconfig.ClusterConfig
			if _, err := os.Stat(path); os.IsNotExist(err) {
				providerKind, cancelled, err := clustercreate.SelectCloudProvider()
				if err != nil {
					return err
				}
				if cancelled {
					return nil
				}
				if providerKind == "google" {
					return fmt.Errorf("google cloud cluster creation is not supported yet")
				}
				// Read or download the latest seed snapshots for determining cluster seed file version to use
				seedManifest, err := depsManager.EnsureSeedSnapshotsCached(context.Background())
				if err != nil {
					return fmt.Errorf("failed to prepare seed snapshots: %w", err)
				}
				// Get cwd for generating files relative to it
				originDir, err := os.Getwd()
				if err != nil {
					return err
				}
				// Triggers `oidc create` flow if no existing issuer
				issuerURL, err := clusterCreateOIDCIssuer(originDir, noApply, autoApprove)
				if err != nil {
					return err
				}
				// Begin cluster-config-related questions
				cfg, err = clustercreate.RunConfigWizard(issuerURL, seedManifest.Seeds.Version)
				if err != nil {
					return err
				}
				if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
					snapshot, ok := seedManifest.Seeds.Snapshots[cfg.Cluster.Seed.Name]
					if !ok {
						return fmt.Errorf("seed snapshot %q was not found in seeds manifest", cfg.Cluster.Seed.Name)
					}
					cfg.Cluster.Seed.Digest = snapshot.Digest
				}
				path, err = infrafiles.ConfirmConfigPath(path, originDir, "cluster config and OpenTofu/Terraform", cfg.Cluster.ID)
				if err != nil {
					return err
				}
				if err := clusterconfig.Write(path, cfg); err != nil {
					return err
				}
				fmt.Printf("Created %s\n", path)
			} else if err != nil {
				return err
			} else {
				cfg, err = clusterconfig.Load(path)
				if err != nil {
					return err
				}
				// Read or download the seed snapshot specified by the cluster config
				if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
					if _, err := depsManager.EnsureSeedSnapshot(context.Background(), cfg.Cluster.Seed.Name, cfg.Cluster.Seed.Version, nil); err != nil {
						return fmt.Errorf("failed to prepare seed snapshot %q: %w", cfg.Cluster.Seed.Name, err)
					}
				}
			}

			// Resolve vmconfig manifests from the local dependency cache. Terraform
			// receives pinned copies so plans remain auditable and deterministic.
			manifests := map[string]tfgen.VMConfigManifest{}
			for poolName, pool := range cfg.Cluster.Pools {
				kind := "knd"
				if poolName == "control-plane" {
					kind = "knc"
				}
				key := kind + "/" + pool.Arch
				if _, ok := manifests[key]; ok {
					continue
				}
				if _, err := depsManager.EnsureVMConfigManifestCached(kind, pool.Arch); err != nil {
					return fmt.Errorf("failed to prepare vmconfig manifest %s: %w", key, err)
				}
				_, raw, err := depsManager.ReadCachedManifest(kind, pool.Arch)
				if err != nil {
					return fmt.Errorf("failed to read vmconfig manifest %s: %w", key, err)
				}
				manifests[key] = tfgen.VMConfigManifest{
					Kind:     kind,
					Arch:     pool.Arch,
					Filename: filepath.Base(depsManager.VMConfigManifestCachePath(kind, pool.Arch)),
					JSON:     raw,
				}
			}
			manifestList := make([]tfgen.VMConfigManifest, 0, len(manifests))
			for _, manifest := range manifests {
				manifestList = append(manifestList, manifest)
			}

			// Generate Terraform files and pinned vmconfig manifest copies.
			dir := filepath.Dir(path)
			if err := tfgen.WriteCluster(path, cfg, tfgen.ClusterOptions{
				DepsMirrorURL:     c.DepsBaseURL(),
				VMConfigManifests: manifestList,
			}); err != nil {
				return err
			}
			fmt.Printf("Generated Podplane OpenTofu/Terraform files in %s\n", dir)
			if noApply {
				return nil
			}
			executor, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			defer cancel()
			if err := executor.Init(ctx, dir); err != nil {
				return err
			}
			ok, err := tui.Confirm("Apply generated OpenTofu/Terraform changes?", autoApprove, 0)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("apply cancelled")
			}
			return executor.Apply(ctx, dir, autoApprove)
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "cluster-config", "f", defaultClusterConfigName, "Path to the cluster config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Generate OpenTofu/Terraform files but do not run apply")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform")
	return cmd
}

// clusterCreateOIDCIssuer collects or creates the OIDC issuer URL needed by a
// new cluster config.
func clusterCreateOIDCIssuer(originDir string, noApply bool, autoApprove bool) (string, error) {
	hasOIDC, err := tui.Confirm("Do you already have an OIDC issuer for this cluster?", false, 0)
	if err != nil {
		return "", err
	}
	if hasOIDC {
		return tui.Input("Cluster OIDC", "OIDC issuer URL", "https://auth.example.com", tui.Required("OIDC issuer URL"))
	}
	createOIDC, err := tui.Confirm("Set up a new Easy OIDC <https://easy-oidc.dev> server now?", false, 0)
	if err != nil {
		return "", err
	}
	if !createOIDC {
		return "", fmt.Errorf("cluster creation requires an OIDC issuer URL; provide an existing issuer or run podplane oidc create first")
	}
	oidcPath, err := filepath.Abs(defaultOIDCConfigName)
	if err != nil {
		return "", err
	}
	oidcPath, err = infrafiles.ConfirmConfigPath(oidcPath, originDir, "OIDC config and OpenTofu/Terraform", "my-oidc-server")
	if err != nil {
		return "", err
	}
	issuerURL, err := oidccreate.Run(context.Background(), oidccreate.Options{
		ConfigPath:  oidcPath,
		NoApply:     noApply,
		AutoApprove: autoApprove,
	})
	if err != nil {
		return "", err
	}
	fmt.Printf("Using new OIDC issuer %s for the cluster config.\n", issuerURL)
	return issuerURL, nil
}
