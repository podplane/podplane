// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterdelete"
	"github.com/podplane/podplane/internal/clusterdelete/aws"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

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
			executor, err := tfexec.NewCLI()
			if err != nil {
				return err
			}
			if len(cfg.Cluster.Providers) == 0 {
				return fmt.Errorf("cluster delete requires at least one provider")
			}
			var provider clusterdelete.Provider
			switch cfg.Cluster.Providers[0].Kind {
			case "aws":
				provider, err = aws.New(context.Background(), cfg.Cluster.Providers[0])
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("cluster delete currently supports AWS clusters only")
			}
			err = clusterdelete.Delete(context.Background(), clusterdelete.DeleteOptions{
				ClusterConfig: cfg,
				StackDir:      dir,
				Executor:      executor,
				Provider:      provider,
				AutoApprove:   autoApprove,
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
