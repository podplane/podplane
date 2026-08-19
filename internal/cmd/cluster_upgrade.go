// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/tfdeps"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tfgen"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

// newClusterUpgradeCmd builds the cluster upgrade command.
func newClusterUpgradeCmd(c *config.Config) *cobra.Command {
	var cfgPath string
	var noApply bool
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade cluster infrastructure dependencies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgPath == "" {
				cfgPath = defaultClusterConfigName
			}
			path, err := filepath.Abs(cfgPath)
			if err != nil {
				return err
			}
			cfg, err := clusterconfig.Load(path)
			if err != nil {
				return err
			}
			depsManager := deps.NewManager(c.DepsBaseURL(), c.DepsCacheDir())
			clusterOptions, err := prepareClusterGeneration(cmd.Context(), c, depsManager, cfg)
			if err != nil {
				return err
			}
			executor, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			dir := filepath.Dir(path)
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer cancel()
			configuration, err := tfdeps.New(c.DepsCacheDir()).Upgrade(ctx, executor, dir)
			if err != nil {
				return err
			}
			clusterOptions.NstanceModuleDir = configuration.NstanceModuleDir
			if err := tfgen.WriteCluster(path, cfg, clusterOptions); err != nil {
				return err
			}
			fmt.Printf("Upgraded Podplane OpenTofu/Terraform dependencies in %s\n", dir)
			if noApply {
				return nil
			}
			executor = executor.WithCLIConfig(configuration.CLIConfigPath)
			applyCtx, applyCancel := context.WithTimeout(context.Background(), 2*time.Hour)
			defer applyCancel()
			if err := executor.Init(applyCtx, dir); err != nil {
				return err
			}
			ok, err := tui.Confirm("Apply upgraded OpenTofu/Terraform dependencies?", autoApprove, 0)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("apply cancelled")
			}
			return executor.Apply(applyCtx, dir, autoApprove)
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "cluster-config", "f", defaultClusterConfigName, "Path to the cluster config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Upgrade generated files but do not run apply")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform")
	return cmd
}
