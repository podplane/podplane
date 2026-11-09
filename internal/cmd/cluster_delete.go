// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
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
				tui.FlowSummary(cmd.Context(), "--no-apply set; cluster config validated and no destroy was run.")
				return nil
			}
			stateBackend, err := tfstate.ForCluster(cfg)
			if err != nil {
				return err
			}
			cloud := cfg.Cluster.Providers[0]
			var provider clusterdelete.CleanupProvider
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
			report := func(e tfexec.Event) {
				if e.Message != "" {
					_, _ = fmt.Fprintln(tui.FlowOutput(cmd.Context()), e.Message)
				}
			}
			executor = executor.WithEvents(func(e tfexec.Event) { report(e) })
			resume := "podplane cluster delete -f '" + strings.ReplaceAll(path, "'", "'\\''") + "'"
			tui.FlowResume(cmd.Context(), resume)
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
			if err := executor.CheckDefaultWorkspace(cmd.Context(), dir); err != nil {
				return err
			}
			var selectedActions clusterdelete.Actions
			var actionLabels []string
			err = clusterdelete.Teardown(cmd.Context(), clusterdelete.TeardownOptions{
				Progress: func(e tfexec.Event) { report(e) },
				Run: func(ctx context.Context, run func(context.Context) error) error {
					return tui.RunPlanAndApply(ctx, tui.PlanAndApplyOptions{
						Title: "Podplane · Delete cluster", Subtitle: cfg.Cluster.ID + " · " + cloud.Kind + " · " + cloud.Region,
						Destroy: true, Tasks: clusterdelete.CleanupTasks(), Resume: resume,
					}, func(ctx context.Context, emit func(tfexec.Event), _ func(tfexec.Plan) (bool, error)) error {
						report = emit
						return run(ctx)
					})
				},
				Engine:           executor,
				Cloud:            provider,
				StateBucketName:  stateBackend.Bucket,
				StateLockKey:     stateBackend.LockKey(),
				CheckStateBucket: func(ctx context.Context) error { return stateBackend.Check(ctx, io.Discard) },
				ConfigPath:       path,
				ConfirmCustom: func(message string) (bool, error) {
					_, _ = fmt.Fprintln(tui.FlowOutput(cmd.Context()), message)
					return tui.Confirm(cmd.Context(), message+"\nProceed with deletion of custom resources?", autoApprove, 1)
				},
				Select: func(labels []string) (clusterdelete.Actions, error) {
					tui.FlowStage(cmd.Context(), "Podplane · Delete cluster "+cfg.Cluster.ID, "Confirm")
					items := make([]tui.ChecklistItem, len(labels))
					for i, label := range labels {
						items[i] = tui.ChecklistItem{Label: label, Checked: true}
						if i > 0 {
							items[i].Requires = []int{i - 1}
						}
					}
					selected, err := tui.Checklist(cmd.Context(), tui.ChecklistOptions{
						Title:         "Podplane · Delete cluster " + cfg.Cluster.ID,
						Description:   "Unchecked actions keep their data and recovery files.\nDeletion is permanent. Later steps require earlier steps.",
						Items:         items,
						ConfirmLabel:  "Delete selected",
						ConfirmPrompt: "Permanently perform selected actions?",
						AutoApprove:   autoApprove,
					})
					if err != nil {
						return clusterdelete.Actions{}, err
					}
					selectedActions, actionLabels = clusterdelete.Actions(selected), labels
					return selectedActions, nil
				},
				DeleteOptions: clusterdelete.DeleteOptions{
					ClusterConfig: cfg,
					StackDir:      dir,
					Executor:      executor,
					Provider:      provider,
					AutoApprove:   autoApprove,
					WaitTimeout:   20 * time.Minute,
					PollInterval:  15 * time.Second,
					Confirm: func(message string) (bool, error) {
						// The checklist explicitly approved instance and infrastructure deletion.
						report(tfexec.Event{Message: message})
						return true, nil
					},
					Status: func(message string) {
						report(tfexec.Event{Message: message})
					},
				},
			})
			if err != nil {
				return err
			}
			if !selectedActions[clusterdelete.Infrastructure] {
				tui.FlowSummary(cmd.Context(), "No deletion actions selected; nothing was removed.")
				return nil
			}
			var receipt strings.Builder
			fmt.Fprintf(&receipt, "Cluster %q cleanup completed.", cfg.Cluster.ID)
			for i, label := range actionLabels {
				result := "Retained"
				if selectedActions[i] {
					result = "Completed"
				}
				fmt.Fprintf(&receipt, "\n  %s: %s", result, label)
			}
			tui.FlowSummary(cmd.Context(), receipt.String())
			return nil
		},
	}
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		parent := cmd.Context()
		defer cmd.SetContext(parent)
		return tui.RunFlow(parent, "Podplane · Delete cluster", func(ctx context.Context) error {
			cmd.SetContext(ctx)
			tui.FlowStage(ctx, "Podplane · Delete cluster", "Prepare")
			return run(cmd, args)
		})
	}
	cmd.Flags().StringVarP(&cfgPath, "cluster-config", "f", defaultClusterConfigName, "Path to the cluster config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Validate config but do not run destroy")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Approve all cleanup actions, including bucket data, state, generated Terraform files, and cluster config")
	return cmd
}
