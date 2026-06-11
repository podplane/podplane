// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deploy"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/health"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy <template>",
	Short: "Deploy an app using a template",
	Example: "  # Deploy the Podplane hello app using the web template\n" +
		"  podplane deploy web --name hello --image ghcr.io/podplane/hello:latest",
	Args: cobra.ExactArgs(1),
}

var (
	deployName        string
	deployImage       string
	deployEnv         []string
	deployHostname    string
	deployPath        string
	deploySet         []string
	deployNamespace   string
	deployContext     string
	deployKubeconfig  string
	deployAutoApprove bool
)

func init() {
	deployCmd.Flags().StringVar(&deployName, "name", "", "Name of the app deployment")
	deployCmd.Flags().StringVar(&deployImage, "image", "", "Container image to deploy")
	deployCmd.Flags().StringArrayVarP(&deployEnv, "env", "e", nil, "Set an environment variable on the app container")
	deployCmd.Flags().StringVar(&deployHostname, "hostname", "", "External hostname for routing")
	deployCmd.Flags().StringVar(&deployPath, "path", "", "URL path prefix for routing")
	deployCmd.Flags().StringArrayVar(&deploySet, "set", nil, "Set a template value using Helm --set syntax")
	deployCmd.Flags().StringVarP(&deployNamespace, "namespace", "n", "", "Kubernetes namespace to deploy into (created if missing)")
	deployCmd.Flags().StringVar(&deployContext, "context", "", "The name of the kubeconfig context to use")
	deployCmd.Flags().StringVar(&deployKubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	deployCmd.Flags().BoolVarP(&deployAutoApprove, "auto-approve", "y", false, "Skip confirmation prompts")
	_ = deployCmd.MarkFlagRequired("name")
	_ = deployCmd.MarkFlagRequired("image")
}

func newDeployCmd(c *config.Config) *cobra.Command {
	deployCmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		chart, err := deploy.EnsureChart(c, args[0], func(download func(func(deps.DownloadEvent)) error) error {
			return tui.RunDownloadProgress("Downloading Podplane dependencies", download)
		})
		if err != nil {
			return fmt.Errorf("resolve template chart: %w", err)
		}
		if err := ensureTemplateDependencies(chart.Template.Dependencies.Components); err != nil {
			return err
		}
		return deploy.Run(deploy.Options{
			Template:   args[0],
			Name:       deployName,
			ChartPath:  chart.Path,
			Image:      deployImage,
			Env:        deployEnv,
			Hostname:   deployHostname,
			Path:       deployPath,
			Set:        deploySet,
			Namespace:  deployNamespace,
			Context:    deployContext,
			Kubeconfig: deployKubeconfig,
		})
	}
	return deployCmd
}

// ensureTemplateDependencies enables any missing component dependencies for a
// template, waits for required CRD HelmReleases, and leaves app components to
// continue reconciling while the app deploy proceeds.
func ensureTemplateDependencies(required []string) error {
	seen := map[string]bool{}
	componentsRequired := []string{}
	for _, value := range required {
		name := strings.TrimSpace(value)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		componentsRequired = append(componentsRequired, name)
	}
	required = componentsRequired
	if len(required) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := components.Read(ctx, deployContext, deployKubeconfig)
	if err != nil {
		return err
	}
	plan, err := resolveTemplateEnablePlan(cfg, required)
	if err != nil {
		return err
	}
	if plan.IsEmpty() {
		return nil
	}

	all := append([]string{}, plan.Apps...)
	all = append(all, plan.CRDs...)
	fmt.Printf("Template requires components that are not installed. Podplane will enable:\n  %s\n", strings.Join(all, ", "))
	ok, err := tui.Confirm("Install missing template dependencies?", deployAutoApprove)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("deploy cancelled")
	}

	if err := components.SetEnabled(ctx, deployContext, deployKubeconfig, plan.Apps, plan.CRDs, true); err != nil {
		return err
	}

	componentItems := cfg.HelmReleaseRefs(plan)
	requiredBeforeDeploy := []tui.StatusProgressItem{}
	for _, item := range componentItems {
		if item.Kind == "crd" {
			requiredBeforeDeploy = append(requiredBeforeDeploy, componentStatusProgressItem(item))
		}
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer waitCancel()
	if err := runComponentStatusProgress(waitCtx, "Installing template dependencies", deployContext, deployKubeconfig, componentItems, requiredBeforeDeploy); err != nil {
		return err
	}
	if len(plan.Apps) > 0 {
		fmt.Println("Component dependencies are reconciling; deploying app once required CRD dependencies are ready.")
	}
	return nil
}

// resolveTemplateEnablePlan combines dependency-resolution plans for each
// top-level component named in a template's dependencies.components list.
func resolveTemplateEnablePlan(cfg *components.Config, required []string) (components.EnableSet, error) {
	apps := map[string]bool{}
	crds := map[string]bool{}
	for _, name := range required {
		plan, err := cfg.ResolveEnable(name)
		if err != nil {
			return components.EnableSet{}, err
		}
		for _, app := range plan.Apps {
			apps[app] = true
		}
		for _, crd := range plan.CRDs {
			crds[crd] = true
		}
	}
	return components.EnableSet{Apps: slices.Sorted(maps.Keys(apps)), CRDs: slices.Sorted(maps.Keys(crds))}, nil
}

// runComponentStatusProgress renders generic status progress for component
// HelmReleases and exits when the required progress items are ready.
func runComponentStatusProgress(ctx context.Context, title, kubeContext, kubeconfig string, componentItems []health.HelmReleaseRef, required []tui.StatusProgressItem) error {
	items := make([]tui.StatusProgressItem, 0, len(componentItems))
	for _, item := range componentItems {
		items = append(items, componentStatusProgressItem(item))
	}
	return tui.RunStatusProgress(title, items, required, func() (map[string]tui.StatusProgressStatus, error) {
		statuses, err := health.ReadHelmReleaseStatuses(ctx, kubeContext, kubeconfig, componentItems)
		if err != nil {
			return nil, err
		}
		return componentStatusProgressStatuses(statuses), nil
	})
}

// componentStatusProgressItem converts a component install item into a generic
// TUI status-progress item.
func componentStatusProgressItem(item health.HelmReleaseRef) tui.StatusProgressItem {
	return tui.StatusProgressItem{
		Key:  item.Namespace + "/helmrelease/" + item.Name,
		Name: item.Name,
		Kind: string(item.Kind),
	}
}

// componentStatusProgressStatuses converts component install statuses into the
// generic status map consumed by the TUI progress renderer.
func componentStatusProgressStatuses(statuses map[string]health.HelmReleaseStatus) map[string]tui.StatusProgressStatus {
	out := map[string]tui.StatusProgressStatus{}
	for key, status := range statuses {
		out[key] = tui.StatusProgressStatus{
			Exists:  status.Exists,
			Ready:   status.Ready,
			Status:  status.Status,
			Message: status.Message,
		}
	}
	return out
}
