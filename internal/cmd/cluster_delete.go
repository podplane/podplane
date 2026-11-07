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
	"github.com/podplane/podplane/internal/clusterdelete"
	"github.com/podplane/podplane/internal/clusterdelete/aws"
	"github.com/podplane/podplane/internal/clusterdelete/google"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/tfdeps"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tfstate"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

// newClusterDeleteCmd wires config validation, confirmation, and AWS or Google Cloud infrastructure destruction.
func newClusterDeleteCmd(c *config.Config) *cobra.Command {
	var cfgPath string
	var noApply bool
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Destroy deployed cluster infrastructure",
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
			dir := filepath.Dir(path)
			if noApply {
				fmt.Println("--no-apply set; cluster config validated and no destroy was run.")
				return nil
			}
			stateBackend, err := tfstate.ForCluster(cfg)
			if err != nil {
				return err
			}
			cloud := cfg.Cluster.Providers[0]
			var provider clusterdelete.Provider
			switch cloud.Kind {
			case "aws":
				provider, err = aws.New(cmd.Context(), cloud)
				if err != nil {
					return err
				}
			case "google":
				p, err := google.New(cmd.Context(), cloud)
				if err != nil {
					return err
				}
				defer func() { _ = p.Close() }()
				provider = p
			}
			executor, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			depsCtx, depsCancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer depsCancel()
			configuration, err := tfdeps.New(c.DepsCacheDir()).Ensure(depsCtx, executor, dir, cloud.Kind)
			if err != nil {
				return err
			}
			if err := stateBackend.WriteConfig(dir); err != nil {
				return err
			}
			executor = executor.WithCLIConfig(configuration.CLIConfigPath)
			if err := executor.Init(cmd.Context(), dir, stateBackend.ConfigFile()); err != nil {
				return err
			}
			err = clusterdelete.Delete(cmd.Context(), clusterdelete.DeleteOptions{
				ClusterConfig: cfg,
				StackDir:      dir,
				Executor:      executor,
				Provider:      provider,
				AutoApprove:   autoApprove,
				WaitTimeout:   20 * time.Minute,
				PollInterval:  15 * time.Second,
				Confirm: func(message string) (bool, error) {
					return tui.Confirm(message, autoApprove, 0)
				},
				Status: func(message string) {
					fmt.Println(message)
				},
			})
			if err != nil {
				return err
			}
			fmt.Printf("Cluster %q destroy completed. Config and generated .tf files were left in place.\n", cfg.Cluster.ID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "cluster-config", "f", defaultClusterConfigName, "Path to the cluster config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Validate config but do not run destroy")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform")
	return cmd
}
