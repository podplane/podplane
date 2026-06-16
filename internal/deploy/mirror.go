// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"fmt"
	"strings"

	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
)

// TemplateMirrorSetArgs builds Helm --set overrides for template image defaults
// that should be served from the cluster's registry mirror.
func TemplateMirrorSetArgs(images []deps.TemplateImage, template string, cluster config.ClusterSummary, explicitAppImage string, userSet []string) []string {
	if cluster.Components.Registry == nil {
		return nil
	}
	mirror := cluster.Components.Registry.Mirror
	if !mirror.Enabled || mirror.Hostname == "" {
		return nil
	}
	explicit := explicitImageValueKeys(explicitAppImage, userSet)
	set := []string{}
	for _, image := range images {
		key := image.Templates[template]
		if key == "" || explicit[key] {
			continue
		}
		set = append(set, fmt.Sprintf("images.%s=%s", key, deps.MirroredImageRef(mirror.Hostname, image.Image)))
	}
	return set
}

// explicitImageValueKeys returns image value keys the user explicitly set, so
// generated mirror overrides do not replace user intent.
func explicitImageValueKeys(explicitAppImage string, userSet []string) map[string]bool {
	explicit := map[string]bool{}
	if explicitAppImage != "" {
		explicit["app"] = true
	}
	for _, value := range userSet {
		name, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		key, ok := strings.CutPrefix(name, "images.")
		if ok && key != "" {
			explicit[key] = true
		}
	}
	return explicit
}
