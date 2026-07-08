// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package registryclient

import (
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
)

func TestRemoteRefDefaultsToAppsBasenameAndTag(t *testing.T) {
	source := mustRef(t, "ghcr.io/acme/example-api:v1.2.3")
	ref, err := remoteRef("registry.example.com", source, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.Name(), "registry.example.com/apps/example-api:v1.2.3"; got != want {
		t.Fatalf("remoteRef() = %q, want %q", got, want)
	}
}

func TestRemoteRefPreservesAppsSourcePath(t *testing.T) {
	source := mustRef(t, "apps/team/example-api:v1.2.3")
	ref, err := remoteRef("registry.example.com", source, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.Name(), "registry.example.com/apps/team/example-api:v1.2.3"; got != want {
		t.Fatalf("remoteRef() = %q, want %q", got, want)
	}
}

func TestSourceRefNormalizesBuildOutputRefs(t *testing.T) {
	tests := map[string]string{
		"api":                                   "apps/api:latest",
		"api:v1":                                "apps/api:v1",
		"apps/api:v1":                           "apps/api:v1",
		"default-registry.local/apps/api:v1":    "apps/api:v1",
		"other-registry.local/apps/team/api:v1": "apps/team/api:v1",
		"default-registry.local/apps/team/api:v1": "apps/team/api:v1",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := sourceRef(input)
			if err != nil {
				t.Fatal(err)
			}
			if sourceDisplay(got) != want {
				t.Fatalf("sourceRef(%q) = %q, want %q", input, sourceDisplay(got), want)
			}
		})
	}
}

func TestSourceRefRejectsRemoteAndReservedRefs(t *testing.T) {
	for _, input := range []string{"ghcr.io/me/api:v1", "mirror/api:v1", "default-registry.local/mirror/api:v1", "default-registry.local/api:v1", "team/api:v1"} {
		t.Run(input, func(t *testing.T) {
			if _, err := sourceRef(input); err == nil {
				t.Fatalf("sourceRef(%q) succeeded, want error", input)
			}
		})
	}
}

func TestDockerImageCandidatesPreferTypedRefThenAppBasename(t *testing.T) {
	source := mustTag(t, "apps/api:v1")
	got := dockerImageCandidates("default-registry.local/apps/api:v1", source)
	want := []string{"default-registry.local/apps/api:v1", "apps/api:v1", "api:v1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dockerImageCandidates() = %#v, want %#v", got, want)
	}
}

func TestRemoteRefPrefixesClusterRegistryForRepositoryOnlyRemote(t *testing.T) {
	source := mustRef(t, "example-api:latest")
	ref, err := remoteRef("registry.example.com", source, "apps/acme/example-api:prod")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.Name(), "registry.example.com/apps/acme/example-api:prod"; got != want {
		t.Fatalf("remoteRef() = %q, want %q", got, want)
	}
}

func TestRemoteRefAllowsExplicitRegistryUnderApps(t *testing.T) {
	source := mustRef(t, "example-api:latest")
	ref, err := remoteRef("registry.example.com", source, "localhost:5000/apps/example-api:prod")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref.Name(), "localhost:5000/apps/example-api:prod"; got != want {
		t.Fatalf("remoteRef() = %q, want %q", got, want)
	}
}

func TestRemoteRefRejectsNonAppsRepositories(t *testing.T) {
	source := mustRef(t, "example-api:latest")
	_, err := remoteRef("registry.example.com", source, "mirror/ghcr.io/acme/example-api:latest")
	if err == nil {
		t.Fatal("remoteRef() succeeded, want apps/** error")
	}
	if !strings.Contains(err.Error(), "must be under apps/**") {
		t.Fatalf("error = %q, want apps/** message", err)
	}
}

func TestRemoteRefRejectsDigest(t *testing.T) {
	source := mustRef(t, "example-api:latest")
	_, err := remoteRef("registry.example.com", source, "apps/example-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil {
		t.Fatal("remoteRef() succeeded, want digest error")
	}
}

func TestImageHasRegistry(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{ref: "apps/example-api:latest", want: false},
		{ref: "example-api:latest", want: false},
		{ref: "registry.example.com/apps/example-api:latest", want: true},
		{ref: "localhost/apps/example-api:latest", want: true},
		{ref: "localhost:5000/apps/example-api:latest", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if got := imageHasRegistry(tt.ref); got != tt.want {
				t.Fatalf("imageHasRegistry(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func mustRef(t *testing.T, ref string) name.Reference {
	t.Helper()
	parsed, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustTag(t *testing.T, ref string) name.Tag {
	t.Helper()
	tag, err := name.NewTag(ref, name.WeakValidation)
	if err != nil {
		t.Fatal(err)
	}
	return tag
}
