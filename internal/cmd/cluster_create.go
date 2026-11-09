// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
				providerKind, cancelled, err := infrafiles.SelectCloudProvider(cmd.Context(), "Which cloud provider?")
				if err != nil {
					return err
				}
				if cancelled {
					return nil
				}
				// Read or download the latest seed snapshots for determining cluster seed file version to use
				_, _ = fmt.Fprintln(tui.FlowOutput(cmd.Context()), "Preparing seed snapshots…")
				seedManifest, err := depsManager.EnsureSeedSnapshotsCached(cmd.Context())
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
				var oidcNotice string
				cfg.Cluster.OIDC.IssuerURL, oidcNotice, err = clusterCreateOIDCIssuer(cmd, originDir, cfg.Cluster.Providers[0], cfg.ResolvedClientID(), noApply, autoApprove)
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
				path, err = infrafiles.ConfirmConfigPath(cmd.Context(), path, originDir, "cluster config and OpenTofu/Terraform", cfg.Cluster.ID)
				if err != nil {
					return err
				}
				if err := clusterconfig.Write(path, cfg); err != nil {
					return err
				}
				resume := "podplane cluster create -f '" + strings.ReplaceAll(path, "'", "'\\''") + "'"
				if noApply {
					resume += " --no-apply"
				}
				_, _ = fmt.Fprintf(tui.FlowOutput(cmd.Context()), "Configuration saved: %s\nResume: %s\n", path, resume)
				tui.FlowSummary(cmd.Context(), oidcNotice)
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

			tui.FlowResume(cmd.Context(), "podplane cluster create -f '"+strings.ReplaceAll(path, "'", "'\\''")+"'")
			tui.FlowStage(cmd.Context(), "Podplane · Create cluster", "Init")
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
			_, _ = fmt.Fprintln(tui.FlowOutput(cmd.Context()), "Preparing OpenTofu/Terraform files and dependencies…")
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
			_, _ = fmt.Fprintf(tui.FlowOutput(cmd.Context()), "Generated OpenTofu/Terraform files in %s\n", dir)
			if noApply {
				tui.FlowSummary(cmd.Context(), "Cluster files generated\n  Config: "+path)
				return nil
			}
			confirm := func(question string) (bool, error) { return tui.Confirm(cmd.Context(), question, autoApprove, 0) }
			if err := stateBackend.Ensure(cmd.Context(), tui.FlowOutput(cmd.Context()), confirm); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Hour)
			defer cancel()
			provider := cfg.Cluster.Providers[0]
			if err := tui.RunPlanAndApply(ctx, tui.PlanAndApplyOptions{
				Title: "Podplane · Create cluster", Subtitle: cfg.Cluster.ID + " · " + provider.Kind + " · " + provider.Region,
				Resume: "podplane cluster create -f '" + strings.ReplaceAll(path, "'", "'\\''") + "'", AutoApprove: autoApprove,
			}, func(ctx context.Context, emit func(tfexec.Event), review func(tfexec.Plan) (bool, error)) error {
				return executor.WithCLIConfig(configuration.CLIConfigPath).PlanAndApply(ctx, dir, stateBackend.ConfigFile(), emit, review)
			}); err != nil {
				return err
			}
			tui.FlowSummary(ctx, "✓ Cluster infrastructure applied: "+cfg.Cluster.ID+"\n  Config: "+path)
			return nil
		},
	}
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		parent := cmd.Context()
		defer cmd.SetContext(parent)
		return tui.RunFlow(parent, "Podplane · Create cluster", func(ctx context.Context) error {
			cmd.SetContext(ctx)
			return run(cmd, args)
		})
	}
	cmd.Flags().StringVarP(&cfgPath, "cluster-config", "f", defaultClusterConfigName, "Path to the cluster config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Generate OpenTofu/Terraform files but do not run apply")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and approve the saved infrastructure plan")
	return cmd
}

// prepareClusterGeneration resolves the cached inputs needed to regenerate a
// cluster's managed Terraform files.
func prepareClusterGeneration(ctx context.Context, c *config.Config, depsManager *deps.Manager, cfg *clusterconfig.ClusterConfig) (tfgen.ClusterOptions, error) {
	if err := tfgen.ValidateClusterGeneration(cfg); err != nil {
		return tfgen.ClusterOptions{}, err
	}
	var seedPath string
	if cfg.Cluster.Seed.Name != "" && cfg.Cluster.Seed.Name != seeds.None {
		var err error
		seedPath, err = depsManager.EnsureSeedSnapshot(ctx, cfg.Cluster.Seed.Name, cfg.Cluster.Seed.Version, nil)
		if err != nil {
			return tfgen.ClusterOptions{}, fmt.Errorf("failed to prepare seed snapshot %q: %w", cfg.Cluster.Seed.Name, err)
		}
		seedPath, err = filepath.Abs(seedPath)
		if err != nil {
			return tfgen.ClusterOptions{}, err
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
		SeedPath:          seedPath,
	}, nil
}

// clusterCreateOIDCIssuer collects or creates the OIDC issuer URL needed by a
// new cluster that logs in with clientID. A new Truster server registers a
// client for clientID; an existing issuer must already have one. It returns a
// notice for the caller to print after the cluster config has been saved.
func clusterCreateOIDCIssuer(cmd *cobra.Command, originDir string, provider clusterconfig.Provider, clientID string, noApply bool, autoApprove bool) (string, string, error) {
	hasOIDC, err := tui.Confirm(cmd.Context(), "Do you already have an OIDC issuer for this cluster?", false, 0)
	if err != nil {
		return "", "", err
	}
	if hasOIDC {
		issuerURL, err := tui.Input(cmd.Context(), "Cluster OIDC", "OIDC issuer URL", "https://auth.example.com", tui.Required("OIDC issuer URL"))
		return issuerURL, fmt.Sprintf("The cluster logs in with OIDC client ID %q; make sure your issuer has a client with that ID.", clientID), err
	}
	setUpOIDC, err := tui.Confirm(cmd.Context(), "Set up a new Truster <https://truster.dev> server now?", false, 0)
	if err != nil {
		return "", "", err
	}
	if !setUpOIDC {
		return "", "", fmt.Errorf("cluster creation requires an OIDC issuer URL; provide an existing issuer or run podplane oidc create first")
	}
	tui.FlowStage(cmd.Context(), "Podplane · Create OIDC server", "Questions")
	defer tui.FlowStage(cmd.Context(), "Podplane · Create cluster", "Questions")
	oidcPath, err := filepath.Abs(defaultOIDCConfigName)
	if err != nil {
		return "", "", err
	}
	oidcPath, err = infrafiles.ConfirmConfigPath(cmd.Context(), oidcPath, originDir, "OIDC config and OpenTofu/Terraform", "my-oidc-server")
	if err != nil {
		return "", "", err
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
		return "", "", err
	}
	return issuerURL, fmt.Sprintf("Using new OIDC issuer %s for the cluster config.", issuerURL), nil
}
