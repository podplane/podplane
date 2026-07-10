// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/podplane/ocimage/pkg/initfile"
	"github.com/podplane/podplane/internal/appbuild"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/local"
	"github.com/podplane/podplane/internal/tui"
	"github.com/spf13/cobra"
)

type podplaneBuildFlags struct {
	file                  string
	tags                  []string
	platform              string
	buildArgs             []string
	labels                []string
	docker                string
	pull                  bool
	sbom                  string
	unsupportedTarget     string
	unsupportedNoCache    bool
	unsupportedProvenance string
	unsupportedOutput     []string
}

// newBuildCmd creates the build command.
func newBuildCmd(c *config.Config) *cobra.Command {
	var flags podplaneBuildFlags
	cmd := &cobra.Command{
		Use:   "build [OPTIONS] [PATH]",
		Short: "Build an OCI container image",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			docker := normalizeDockerFlag(flags.docker)
			if flags.unsupportedTarget != "" {
				return fmt.Errorf("--target is not supported because podplane build does not support multi-stage builds")
			}
			if flags.unsupportedNoCache {
				return fmt.Errorf("--no-cache is not supported because podplane build packages prebuilt files and does not maintain a build cache")
			}
			if flags.unsupportedProvenance != "" {
				return fmt.Errorf("--provenance is not supported yet")
			}
			if len(flags.unsupportedOutput) > 0 {
				return fmt.Errorf("--output is not supported yet; podplane build stores images in the local Podplane registry cache")
			}
			sbom, err := strconv.ParseBool(flags.sbom)
			if err != nil {
				return fmt.Errorf("--sbom=%s is not supported; use a boolean value", flags.sbom)
			}
			ctxDir := "."
			if len(args) == 1 {
				ctxDir = args[0]
			}
			registryHost := local.LocalRegistryHostname("default")
			storeRoot := deps.NewManager(c.DepsBaseURL(), c.DepsCacheDir()).RegistryCacheDir()
			progress := func(msg string) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "=> %s\n", msg)
			}
			res, err := appbuild.Build(cmd.Context(), appbuild.Options{ContextDir: ctxDir, File: flags.file, Tags: flags.tags, Platform: flags.platform, Arch: c.Arch(), BuildArgs: flags.buildArgs, Labels: flags.labels, StoreRoot: storeRoot, DefaultRegistryHost: registryHost, Docker: docker, Pull: flags.pull, SBOM: sbom, ChooseTemplate: selectBuildTemplate, Progress: progress})
			if err != nil {
				return err
			}
			printGeneratedBuildFiles(cmd, res)
			return printBuildSuccess(cmd, res.DisplayTags, res.SBOMs)
		},
	}
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "name of the Containerfile or Dockerfile")
	cmd.Flags().StringArrayVarP(&flags.tags, "tag", "t", nil, "name and optionally a tag")
	cmd.Flags().StringVar(&flags.platform, "platform", "", "target platform(s), comma-separated")
	cmd.Flags().StringArrayVar(&flags.buildArgs, "build-arg", nil, "set build-time variables")
	cmd.Flags().StringArrayVar(&flags.labels, "label", nil, "set image labels")
	cmd.Flags().StringVar(&flags.docker, "docker", "docker", "use Docker Buildx fallback, optionally with a Docker binary path; use --docker=false to disable")
	cmd.Flags().Lookup("docker").NoOptDefVal = "docker"
	cmd.Flags().BoolVar(&flags.pull, "pull", false, "always attempt to pull base images instead of using the local OCI store first")
	cmd.Flags().StringVar(&flags.sbom, "sbom", "false", "generate an SBOM attestation with syft")
	cmd.Flags().Lookup("sbom").NoOptDefVal = "true"
	cmd.Flags().StringVar(&flags.unsupportedTarget, "target", "", "unsupported")
	cmd.Flags().BoolVar(&flags.unsupportedNoCache, "no-cache", false, "unsupported")
	cmd.Flags().StringVar(&flags.unsupportedProvenance, "provenance", "", "unsupported")
	cmd.Flags().StringArrayVarP(&flags.unsupportedOutput, "output", "o", nil, "unsupported")
	return cmd
}

// normalizeDockerFlag converts the build --docker flag into an ocimage Docker option.
func normalizeDockerFlag(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false", "none", "no", "off":
		return ""
	case "true":
		return "docker"
	default:
		return value
	}
}

// selectBuildTemplate prompts the user to choose a detected build template.
func selectBuildTemplate(matches []initfile.Match) (initfile.Match, bool, error) {
	items := make([]list.Item, 0, len(matches)+1)
	byID := map[string]initfile.Match{}
	for _, match := range matches {
		byID[match.Template.ID] = match
		items = append(items, tui.Item{Key: match.Template.ID, Label: fmt.Sprintf("%s — %s", match.Template.Name, match.Reason)})
	}
	items = append(items, tui.Item{Key: "", Label: "Cancel", Cancel: true})
	choice, quitting, err := tui.SelectList("Generate Containerfile", "Choose a Containerfile template", items)
	if err != nil {
		return initfile.Match{}, false, err
	}
	if quitting || choice == "" {
		return initfile.Match{}, false, nil
	}
	return byID[choice], true, nil
}

// printGeneratedBuildFiles reports Containerfile generation performed before build.
func printGeneratedBuildFiles(cmd *cobra.Command, res *appbuild.Result) {
	if len(res.GeneratedFiles) == 0 {
		return
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No Containerfile or Dockerfile found.")
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Detected %s.\n\n", res.DetectedReason)
	for _, path := range res.GeneratedFiles {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created ./%s for you.\n", path)
	}
}

// printBuildSuccess prints image build results and local deploy next steps.
func printBuildSuccess(cmd *cobra.Command, tags []string, sboms []string) error {
	for _, tag := range tags {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "\n✓ Built image %s\n", tag); err != nil {
			return err
		}
	}
	for _, tag := range sboms {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Generated SBOM for %s\n", tag); err != nil {
			return err
		}
	}
	if len(tags) == 0 {
		return nil
	}
	name := appbuild.DeployNameFromImage(tags[0])
	_, err := fmt.Fprintf(cmd.OutOrStdout(), `
Deploy it locally:

  podplane deploy web --name %s \
    --image %s \
    --hostname %s.%s

View logs:

  podplane logs %s

Open a shell:

  podplane shell %s
`, name, tags[0], name, appbuild.RegistryClusterID(tags[0])+".localhost", name, name)
	return err
}
