// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/kubectl"
)

// SetEnabled patches the platform-components HelmRelease so the given apps
// and CRDs have spec.values.platform.components.{apps,crds}.<name>.enabled
// set to the given value. Empty lists are no-ops. kubeContext and
// kubeconfig are optional; empty values use kubectl's defaults.
func SetEnabled(ctx context.Context, kubeContext, kubeconfig string, apps, crds []string, enabled bool) error {
	if len(apps) == 0 && len(crds) == 0 {
		return nil
	}
	patch := buildEnabledPatch(apps, crds, enabled)
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	args := []string{
		"patch", "helmrelease",
		HelmReleaseName,
		"-n", HelmReleaseNamespace,
		"--type", "merge",
		"-p", string(raw),
	}
	args = append(kubectl.Args(kubeContext, kubeconfig), args...)
	var stdout, stderr bytes.Buffer
	cmd := execwrap.Command("kubectl", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &kubectl.Error{Stage: "patch helmrelease", Err: err, Stderr: stderr.String()}
	}
	_ = ctx
	return nil
}

// buildEnabledPatch builds the merge-patch JSON body that flips enabled for
// the listed apps and CRDs.
func buildEnabledPatch(apps, crds []string, enabled bool) map[string]any {
	componentsPatch := map[string]any{}
	if len(apps) > 0 {
		appsPatch := map[string]any{}
		for _, name := range apps {
			appsPatch[name] = map[string]any{"enabled": enabled}
		}
		componentsPatch["apps"] = appsPatch
	}
	if len(crds) > 0 {
		crdsPatch := map[string]any{}
		for _, name := range crds {
			crdsPatch[name] = map[string]any{"enabled": enabled}
		}
		componentsPatch["crds"] = crdsPatch
	}
	return map[string]any{
		"spec": map[string]any{
			"values": map[string]any{
				"platform": map[string]any{
					"components": componentsPatch,
				},
			},
		},
	}
}
