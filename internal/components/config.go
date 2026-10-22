// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/podplane/podplane/internal/clusterconfig"
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
	Apps      map[string]Entry
	CRDs      map[string]Entry
	ClusterID string
	Secrets   clusterconfig.Secrets
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
	return ParseValues(stdout.Bytes())
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

// ParseValues decodes merged Helm values JSON from a prepared seed or
// `helm get values --all -o json`, without a spec.values wrapper.
func ParseValues(raw []byte) (*Config, error) {
	// Decode only the chart fields used by the CLI. Helm's nesting and
	// camelCase provider keys stay at this boundary, not in Config consumers.
	var obj struct {
		Platform struct {
			Components *struct {
				Apps   map[string]Entry
				CRDs   map[string]Entry
				Values struct {
					Operator struct {
						Podplane struct {
							Operator struct {
								Config struct {
									Cluster struct{ ID string }
									Secrets struct {
										DefaultProvider string
										Providers       map[string]struct {
											Kind       string
											KeyPrefix  string
											ObjectType string
											Region     string
											ProjectID  string
											Location   string
											Address    string
											MountPath  string
										}
									}
								}
							}
						}
					} `json:"podplane-operator"`
				}
			}
		}
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("decode platform-components values: %w", err)
	}
	components := obj.Platform.Components
	if components == nil {
		return nil, fmt.Errorf("platform-components values are missing platform.components")
	}
	operator := components.Values.Operator.Podplane.Operator.Config
	cfg := &Config{
		Apps:      components.Apps,
		CRDs:      components.CRDs,
		ClusterID: operator.Cluster.ID,
		Secrets: clusterconfig.Secrets{
			DefaultProvider: operator.Secrets.DefaultProvider,
			Providers:       make(map[string]clusterconfig.SecretsProvider, len(operator.Secrets.Providers)),
		},
	}
	if cfg.Apps == nil {
		cfg.Apps = map[string]Entry{}
	}
	if cfg.CRDs == nil {
		cfg.CRDs = map[string]Entry{}
	}
	for name, p := range operator.Secrets.Providers {
		cfg.Secrets.Providers[name] = clusterconfig.SecretsProvider{
			Kind: p.Kind, KeyPrefix: p.KeyPrefix,
			ObjectType: p.ObjectType, Region: p.Region,
			ProjectID: p.ProjectID, Location: p.Location,
			Address: p.Address, MountPath: p.MountPath,
		}
	}
	return cfg, nil
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
	if e.ReleaseNotFound() {
		return "platform-components is not installed in this cluster; this looks like a bare Podplane cluster. App deploys and component installs require a cluster seeded with the minimal, platform, or agentic platform components. Bootstrap platform-components manually or recreate the cluster with minimal/platform/agentic components, then retry."
	}
	if e.Stderr == "" {
		return fmt.Sprintf("helm %s: %v", e.Stage, e.Err)
	}
	return fmt.Sprintf("helm %s: %v: %s", e.Stage, e.Err, e.Stderr)
}

// ReleaseNotFound reports whether helm failed because the platform-components
// release does not exist in the cluster.
func (e *HelmError) ReleaseNotFound() bool {
	if e == nil {
		return false
	}
	return strings.Contains(e.Stderr, "release: not found")
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
