// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package appbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ocibuild "github.com/podplane/ocimage/pkg/build"
	"github.com/podplane/ocimage/pkg/initfile"
)

// NoBuildFileError is the user-facing missing build file error message.
const NoBuildFileError = `No Containerfile or Dockerfile found.

Podplane builds images from a Containerfile or Dockerfile. Containerfile is the
OCI-neutral name for what many tools historically called a Dockerfile, but you
can still use Dockerfile if you prefer.

I couldn't safely generate one for this project.

Create a Containerfile or Dockerfile, then run:

  podplane build`

// TemplateChooser selects one template from multiple detected options.
type TemplateChooser func([]initfile.Match) (initfile.Match, bool, error)

// Options controls a Podplane app image build.
type Options struct {
	ContextDir          string
	File                string
	Tags                []string
	Platform            string
	Arch                string
	BuildArgs           []string
	Labels              []string
	StoreRoot           string
	DefaultRegistryHost string
	Docker              string
	Pull                bool
	SBOM                bool
	ChooseTemplate      TemplateChooser
	Progress            func(string)
}

// Result describes a completed Podplane app image build.
type Result struct {
	GeneratedFiles []string
	DetectedReason string
	DisplayTags    []string
	SBOMs          []string
}

// Build creates an app image in Podplane's local registry cache.
func Build(ctx context.Context, opts Options) (*Result, error) {
	ctxDir := opts.ContextDir
	if ctxDir == "" {
		ctxDir = "."
	}
	ctxDir, err := filepath.Abs(ctxDir)
	if err != nil {
		return nil, fmt.Errorf("resolve build context: %w", err)
	}
	generated, reason, err := EnsureBuildFile(ctxDir, opts.File, opts.ChooseTemplate)
	if err != nil {
		return nil, err
	}
	tags := opts.Tags
	if len(tags) == 0 {
		appName, err := DetectAppName(ctxDir)
		if err != nil {
			return nil, err
		}
		tags = []string{appName}
	}
	storeTags, displayTags, err := NormalizeTags(tags, opts.DefaultRegistryHost)
	if err != nil {
		return nil, err
	}
	buildArgs, err := ParseKeyValues(opts.BuildArgs)
	if err != nil {
		return nil, err
	}
	labels, err := ParseKeyValues(opts.Labels)
	if err != nil {
		return nil, err
	}
	platforms := []string{"linux/" + opts.Arch}
	if opts.Platform != "" {
		platforms = []string{opts.Platform}
	}
	res, err := ocibuild.Build(ctx, ocibuild.Options{ContextDir: ctxDir, File: opts.File, Tags: storeTags, Platforms: platforms, BuildArgs: buildArgs, Labels: labels, StoreRoot: opts.StoreRoot, Pull: opts.Pull, SBOM: opts.SBOM, Docker: opts.Docker, Progress: opts.Progress})
	if err != nil {
		return nil, err
	}
	return &Result{GeneratedFiles: generated, DetectedReason: reason, DisplayTags: displayTags, SBOMs: res.SBOMs}, nil
}

// EnsureBuildFile creates a conservative Containerfile when no build file exists.
func EnsureBuildFile(ctxDir string, file string, choose TemplateChooser) ([]string, string, error) {
	if file != "" {
		return nil, "", nil
	}
	for _, name := range []string{"Dockerfile", "Containerfile"} {
		if _, err := os.Stat(filepath.Join(ctxDir, name)); err == nil {
			return nil, "", nil
		}
	}

	matches, err := initfile.Detect(ctxDir)
	if err != nil {
		return nil, "", err
	}
	selfContained := matches[:0]
	for _, match := range matches {
		if match.Template.SelfContained {
			selfContained = append(selfContained, match)
		}
	}
	if len(selfContained) == 0 {
		return nil, "", fmt.Errorf("%s", NoBuildFileError)
	}
	match := selfContained[0]
	if len(selfContained) > 1 {
		if choose == nil {
			return nil, "", fmt.Errorf("detected multiple supported project templates; choose one explicitly")
		}
		selected, ok, err := choose(selfContained)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", fmt.Errorf("build cancelled")
		}
		match = selected
	}
	written, err := initfile.Write(ctxDir, match.Template, false)
	return written, match.Reason, err
}

// ParseKeyValues parses repeated KEY=VALUE flags.
func ParseKeyValues(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", value)
		}
		out[key] = val
	}
	return out, nil
}
