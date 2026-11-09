// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/oidcconfig"
	"github.com/podplane/podplane/internal/oidcdelete"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tfstate"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

// newOIDCDeleteCmd builds the OIDC delete command and delegates destroy
// workflow behavior to the oidcdelete package.
func newOIDCDeleteCmd(c *config.Config) *cobra.Command {
	var cfgPath string
	var noApply bool
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Destroy deployed OIDC infrastructure",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgPath == "" {
				cfgPath = defaultOIDCConfigName
			}
			path, err := filepath.Abs(cfgPath)
			if err != nil {
				return err
			}
			cfg, err := oidcconfig.Load(path)
			if err != nil {
				return err
			}
			if noApply {
				tui.FlowSummary(cmd.Context(), "--no-apply set; OIDC config validated and no destroy was run.")
				return nil
			}
			stateBackend, err := tfstate.ForOIDC(cfg)
			if err != nil {
				return err
			}
			if err := stateBackend.Check(cmd.Context(), tui.FlowOutput(cmd.Context())); err != nil {
				return err
			}
			dir := filepath.Dir(path)
			if err := stateBackend.WriteConfig(dir); err != nil {
				return err
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
			resume := "podplane oidc delete -f '" + strings.ReplaceAll(path, "'", "'\\''") + "'"
			tui.FlowResume(cmd.Context(), resume)
			if err := executor.Init(cmd.Context(), dir, stateBackend.ConfigFile()); err != nil {
				return err
			}
			if err := oidcdelete.Delete(cmd.Context(), oidcdelete.DeleteOptions{
				Run: func(ctx context.Context, run func(context.Context) error) error {
					return tui.RunPlanAndApply(ctx, tui.PlanAndApplyOptions{
						Title: "Podplane · Delete OIDC server", Subtitle: cfg.IssuerURL(), Resume: resume, Destroy: true,
						Tasks: []string{"Destroy OIDC infrastructure", "Retain state and local files"},
					}, func(ctx context.Context, emit func(tfexec.Event), _ func(tfexec.Plan) (bool, error)) error {
						report = emit
						emit(tfexec.Event{Stage: "Delete", Type: "task_start", Address: "Destroy OIDC infrastructure"})
						if err := run(ctx); err != nil {
							return err
						}
						emit(tfexec.Event{Type: "task_complete", Address: "Destroy OIDC infrastructure"})
						emit(tfexec.Event{Stage: "Cleanup", Type: "task_skip", Address: "Retain state and local files", Message: "State bucket, config and generated files retained."})
						return nil
					})
				},
				StackDir: dir,
				Executor: executor,
				Confirm: func(message string) (bool, error) {
					tui.FlowStage(cmd.Context(), "Podplane · Delete OIDC server", "Confirm")
					return tui.Confirm(cmd.Context(), message+"\nState bucket, config and generated files will be retained.", autoApprove, 1)
				},
			}); err != nil {
				return err
			}
			tui.FlowSummary(cmd.Context(), "OIDC infrastructure deleted. State bucket, config and generated .tf files retained.")
			return nil
		},
	}
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		parent := cmd.Context()
		defer cmd.SetContext(parent)
		return tui.RunFlow(parent, "Podplane · Delete OIDC server", func(ctx context.Context) error {
			cmd.SetContext(ctx)
			tui.FlowStage(ctx, "Podplane · Delete OIDC server", "Prepare")
			return run(cmd, args)
		})
	}
	cmd.Flags().StringVarP(&cfgPath, "oidc-config", "f", defaultOIDCConfigName, "Path to the OIDC config file")
	cmd.Flags().BoolVar(&noApply, "no-apply", false, "Validate config but do not run destroy")
	cmd.Flags().BoolVarP(&autoApprove, "auto-approve", "y", false, "Skip confirmation prompts and pass auto-approval to OpenTofu/Terraform")
	return cmd
}
