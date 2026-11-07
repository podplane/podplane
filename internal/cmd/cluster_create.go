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
	"github.com/podplane/podplane/internal/oidcconfig"
	"github.com/podplane/podplane/internal/oidccreate"
	"github.com/podplane/podplane/internal/tfdeps"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tfgen"
	"github.com/podplane/podplane/internal/tfstate"
	"github.com/podplane/podplane/internal/tui"
	"github.com/podplane/podplane/pkg/seeds"
	"github.com/spf13/cobra"
)

// newClusterCreateCmd builds the cluster create command and wires config
// collection, dependency preparation, Terraform generation, and optional apply.
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
			usedWizard := false
			if _, err := os.Stat(path); os.IsNotExist(err) {
				providerKind, cancelled, err := infrafiles.SelectCloudProvider("Which cloud provider?")
				if err != nil {
					return err
				}
				if cancelled {
					return nil
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
				// Begin cluster-config-related questions
				cfg, err = clustercreate.RunConfigWizard(cmd.Context(), providerKind, seedManifest.Seeds.Version)
				if err != nil {
					return err
				}
				usedWizard = true
				if providerKind == "aws" {
					provider := &cfg.Cluster.Providers[0]
					provider.Account, err = infrafiles.ConfirmAWSAccount(cmd.Context(), provider.Region, provider.Profile, false)
					if err != nil {
						return err
					}
				}
				// Triggers `oidc create` flow if no existing issuer
				cfg.Cluster.OIDC.IssuerURL, err = clusterCreateOIDCIssuer(cmd, originDir, cfg.Cluster.Providers[0], cfg.ResolvedClientID(), noApply, autoApprove)
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
				provider := &cfg.Cluster.Providers[0]
				if provider.Kind == "aws" && provider.Account == "" {
					provider.Account, err = infrafiles.ConfirmAWSAccount(cmd.Context(), provider.Region, provider.Profile, autoApprove)
					if err != nil {
						return err
					}
					if err := clusterconfig.Write(path, cfg); err != nil {
						return err
					}
				}
			}

			stateBackend, err := tfstate.ForCluster(cfg)
			if err != nil {
				return err
			}
			if !noApply {
				// The wizard already checked DNS or the user explicitly skipped it.
				if !usedWizard {
					if err := clustercreate.VerifyDNS(cmd.Context(), cfg, autoApprove); err != nil {
						return err
					}
				}
				if err := tui.RunVerification(cmd.Context(), "Checking cloud access", "Remote-state bucket: "+stateBackend.Bucket, stateBackend.Preflight, autoApprove); err != nil {
					return err
				}
			}

			// Generate Terraform files and pinned vmconfig manifest copies.
			dir := filepath.Dir(path)
			clusterOptions, err := prepareClusterGeneration(cmd.Context(), c, depsManager, cfg)
			if err != nil {
				return err
			}
			executor, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			depsCtx, depsCancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer depsCancel()
			configuration, err := tfdeps.New(c.DepsCacheDir()).Ensure(depsCtx, executor, dir, cfg.Cluster.Providers[0].Kind)
			if err != nil {
				return err
			}
			clusterOptions.NstanceModuleDir = configuration.NstanceModuleDir
			if err := tfgen.WriteCluster(path, cfg, clusterOptions); err != nil {
				return err
			}
			if err := stateBackend.WriteConfig(dir); err != nil {
				return err
			}
			fmt.Printf("Generated Podplane OpenTofu/Terraform files in %s\n", dir)
			if noApply {
				return nil
			}
			confirm := func(question string) (bool, error) { return tui.Confirm(question, autoApprove, 0) }
			if err := stateBackend.Ensure(cmd.Context(), cmd.ErrOrStderr(), confirm); err != nil {
				return err
			}
			ok, err := confirm("Apply generated OpenTofu/Terraform changes?")
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("apply cancelled")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Hour)
			defer cancel()
			return executor.WithCLIConfig(configuration.CLIConfigPath).InitApply(ctx, dir, stateBackend.ConfigFile(), autoApprove)
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "cluster-config", "f", defaultClusterConfigName, "Path to the cluster config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Generate OpenTofu/Terraform files but do not run apply")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform")
	return cmd
}

// prepareClusterGeneration resolves the cached inputs needed to regenerate a
// cluster's managed Terraform files.
func prepareClusterGeneration(ctx context.Context, c *config.Config, depsManager *deps.Manager, cfg *clusterconfig.ClusterConfig) (tfgen.ClusterOptions, error) {
	if err := tfgen.ValidateClusterGeneration(cfg); err != nil {
		return tfgen.ClusterOptions{}, err
	}
	if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
		if _, err := depsManager.EnsureSeedSnapshot(ctx, cfg.Cluster.Seed.Name, cfg.Cluster.Seed.Version, nil); err != nil {
			return tfgen.ClusterOptions{}, fmt.Errorf("failed to prepare seed snapshot %q: %w", cfg.Cluster.Seed.Name, err)
		}
	}

	manifestList := []tfgen.VMConfigManifest{}
	for _, target := range tfgen.RequiredVMTargets(cfg) {
		key := target.Kind + "/" + target.Arch
		if _, err := depsManager.EnsureVMConfigManifestCached(target.Kind, target.Arch); err != nil {
			return tfgen.ClusterOptions{}, fmt.Errorf("failed to prepare vmconfig manifest %s: %w", key, err)
		}
		_, raw, err := depsManager.ReadCachedManifest(target.Kind, target.Arch)
		if err != nil {
			return tfgen.ClusterOptions{}, fmt.Errorf("failed to read vmconfig manifest %s: %w", key, err)
		}
		manifestList = append(manifestList, tfgen.VMConfigManifest{
			Kind:     target.Kind,
			Arch:     target.Arch,
			Filename: filepath.Base(depsManager.VMConfigManifestCachePath(target.Kind, target.Arch)),
			JSON:     raw,
		})
	}
	return tfgen.ClusterOptions{
		DepsMirrorURL:     c.DepsBaseURL(),
		VMConfigManifests: manifestList,
	}, nil
}

// clusterCreateOIDCIssuer collects or creates the OIDC issuer URL needed by a
// new cluster that logs in with clientID. A new Truster server registers a
// client for clientID; an existing issuer must already have one.
func clusterCreateOIDCIssuer(cmd *cobra.Command, originDir string, provider clusterconfig.Provider, clientID string, noApply bool, autoApprove bool) (string, error) {
	hasOIDC, err := tui.Confirm("Do you already have an OIDC issuer for this cluster?", false, 0)
	if err != nil {
		return "", err
	}
	if hasOIDC {
		issuerURL, err := tui.Input("Cluster OIDC", "OIDC issuer URL", "https://auth.example.com", tui.Required("OIDC issuer URL"))
		if err == nil {
			fmt.Printf("The cluster logs in with OIDC client ID %q; make sure your issuer has a client with that ID.\n", clientID)
		}
		return issuerURL, err
	}
	setUpOIDC, err := tui.Confirm("Set up a new Truster <https://truster.dev> server now?", false, 0)
	if err != nil {
		return "", err
	}
	if !setUpOIDC {
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
	oidcProvider := oidcconfig.Provider{
		Kind: provider.Kind, Region: provider.Region, Account: provider.Account,
		Profile: provider.Profile, Project: provider.Project,
	}
	// The wizard selects one zone; keep the choice deterministic if a caller
	// supplies more than one.
	if provider.Kind == "google" {
		for zone := range provider.Zones {
			if oidcProvider.Zone == "" || zone < oidcProvider.Zone {
				oidcProvider.Zone = zone
			}
		}
	}
	issuerURL, err := oidccreate.Run(cmd.Context(), oidccreate.Options{
		ConfigPath:  oidcPath,
		Provider:    &oidcProvider,
		ClientID:    clientID,
		NoApply:     noApply,
		AutoApprove: autoApprove,
	})
	if err != nil {
		return "", err
	}
	fmt.Printf("Using new OIDC issuer %s for the cluster config.\n", issuerURL)
	return issuerURL, nil
}
