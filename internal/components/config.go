// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/podplane/podplane/internal/execwrap"
)

// Helm release coordinates for the platform-components release that is the
// runtime source of truth for which components are enabled.
const (
	HelmReleaseName      = "platform-components"
	HelmReleaseNamespace = "platform-components"
)

// Config is a snapshot of the platform-components Helm release values that
// determine which Podplane components are enabled in a cluster.
type Config struct {
	Apps map[string]Entry
	CRDs map[string]Entry
}

// Entry describes one component (app or CRD chart) entry as read from the
// platform-components Helm release values.
type Entry struct {
	Enabled   bool
	Namespace string
	Core      bool
	DependsOn []string
}

// Read returns the platform-components Helm release values, merging chart
// defaults with any overrides applied by the platform-components
// HelmRelease. This is what makes `core: true` (declared as a chart default
// in podplane/components) visible to the CLI even though the HelmRelease
// only stores user overrides.
//
// kubeContext and kubeconfig are optional; empty values use helm's defaults.
func Read(ctx context.Context, kubeContext, kubeconfig string) (*Config, error) {
	args := []string{
		"get", "values",
		HelmReleaseName,
		"-n", HelmReleaseNamespace,
		"--all",
		"-o", "json",
	}
	args = append(helmArgs(kubeContext, kubeconfig), args...)
	var stdout, stderr bytes.Buffer
	cmd := execwrap.Command("helm", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &HelmError{Stage: "get values", Err: err, Stderr: stderr.String()}
	}
	_ = ctx
	return parseValues(stdout.Bytes())
}

// Has returns true when name exists in either Apps or CRDs.
func (c *Config) Has(name string) bool {
	if c == nil {
		return false
	}
	if _, ok := c.Apps[name]; ok {
		return true
	}
	_, ok := c.CRDs[name]
	return ok
}

// Get returns the entry for name. isApp is true when the entry is an app
// chart, false when it is a CRD chart. ok is false when name is not present.
func (c *Config) Get(name string) (entry Entry, isApp bool, ok bool) {
	if c == nil {
		return Entry{}, false, false
	}
	if e, found := c.Apps[name]; found {
		return e, true, true
	}
	if e, found := c.CRDs[name]; found {
		return e, false, true
	}
	return Entry{}, false, false
}

// parseValues decodes the JSON output of `helm get values --all -o json`
// into a Config. The input is the merged values structure (no spec.values
// wrapper).
func parseValues(raw []byte) (*Config, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode platform-components values: %w", err)
	}
	platform, _ := obj["platform"].(map[string]any)
	componentsNode, _ := platform["components"].(map[string]any)
	if componentsNode == nil {
		return nil, fmt.Errorf("platform-components values are missing platform.components")
	}
	appsNode, _ := componentsNode["apps"].(map[string]any)
	crdsNode, _ := componentsNode["crds"].(map[string]any)
	cfg := &Config{
		Apps: map[string]Entry{},
		CRDs: map[string]Entry{},
	}
	for name, raw := range appsNode {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cfg.Apps[name] = entryFromMap(entry)
	}
	for name, raw := range crdsNode {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cfg.CRDs[name] = entryFromMap(entry)
	}
	return cfg, nil
}

// entryFromMap converts one entry's raw map into an Entry.
func entryFromMap(raw map[string]any) Entry {
	entry := Entry{}
	if v, ok := raw["enabled"].(bool); ok {
		entry.Enabled = v
	}
	if v, ok := raw["namespace"].(string); ok {
		entry.Namespace = v
	}
	if v, ok := raw["core"].(bool); ok {
		entry.Core = v
	}
	if v, ok := raw["dependsOn"].([]any); ok {
		for _, dep := range v {
			if s, ok := dep.(string); ok {
				entry.DependsOn = append(entry.DependsOn, s)
			}
		}
	}
	return entry
}

// HelmError is returned when a helm invocation fails. It captures the stage
// (so callers can format helpful messages) and the helm stderr.
type HelmError struct {
	Stage  string
	Err    error
	Stderr string
}

// Error implements the error interface.
func (e *HelmError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("helm %s: %v", e.Stage, e.Err)
	}
	return fmt.Sprintf("helm %s: %v: %s", e.Stage, e.Err, e.Stderr)
}

// Unwrap returns the underlying exec error so callers can use errors.Is/As.
func (e *HelmError) Unwrap() error {
	return e.Err
}

// helmArgs returns the leading helm flags for context/kubeconfig. helm uses
// --kube-context, not --context.
func helmArgs(kubeContext, kubeconfig string) []string {
	args := []string{}
	if kubeContext != "" {
		args = append(args, "--kube-context", kubeContext)
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	return args
}
