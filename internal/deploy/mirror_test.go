// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"slices"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/deps"
)

func TestTemplateMirrorSetArgsInjectsTemplateImages(t *testing.T) {
	t.Parallel()
	images := []deps.TemplateImage{
		{Image: "ghcr.io/podplane/hello:latest", Templates: map[string]string{"web": "app"}},
		{Image: "docker.io/library/caddy:2", Templates: map[string]string{"web": "caddy"}},
		{Image: "redis:7", Templates: map[string]string{"worker": "redis"}},
	}
	cluster := mirrorClusterSummary("zot.local")

	got := TemplateMirrorSetArgs(images, "web", cluster, "", nil)
	want := []string{
		"images.app=zot.local/ghcr.io/podplane/hello:latest",
		"images.caddy=zot.local/docker.io/library/caddy:2",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("TemplateMirrorSetArgs() = %#v, want %#v", got, want)
	}
}

func TestTemplateMirrorSetArgsSkipsExplicitImageValues(t *testing.T) {
	t.Parallel()
	images := []deps.TemplateImage{
		{Image: "ghcr.io/podplane/hello:latest", Templates: map[string]string{"web": "app"}},
		{Image: "docker.io/library/caddy:2", Templates: map[string]string{"web": "caddy"}},
	}
	cluster := mirrorClusterSummary("zot.local")

	got := TemplateMirrorSetArgs(images, "web", cluster, "custom/app:latest", []string{"images.caddy=custom/caddy:2"})
	if len(got) != 0 {
		t.Fatalf("TemplateMirrorSetArgs() = %#v, want no generated overrides", got)
	}
}

func TestTemplateMirrorSetArgsDisabledWithoutMirror(t *testing.T) {
	t.Parallel()
	images := []deps.TemplateImage{{Image: "docker.io/library/caddy:2", Templates: map[string]string{"web": "caddy"}}}

	got := TemplateMirrorSetArgs(images, "web", config.ClusterSummary{}, "", nil)
	if len(got) != 0 {
		t.Fatalf("TemplateMirrorSetArgs() = %#v, want no generated overrides", got)
	}
}

func mirrorClusterSummary(hostname string) config.ClusterSummary {
	return config.ClusterSummary{
		Components: config.ClusterSummaryClusterComponents{
			Registry: &clusterconfig.ComponentsRegistry{
				Mirror: clusterconfig.ComponentsRegistryMirror{Enabled: true, Hostname: hostname},
			},
		},
	}
}
