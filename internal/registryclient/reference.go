// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package registryclient

import (
	"fmt"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// sourceRef normalizes a Podplane app image reference to its registry-cache tag.
func sourceRef(source string) (name.Tag, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return name.Tag{}, fmt.Errorf("image name cannot be empty")
	}
	if strings.Contains(source, "@") {
		return name.Tag{}, fmt.Errorf("podplane push sources must use NAME[:TAG], not digest references")
	}
	parts := strings.SplitN(source, "/", 2)
	pathTag := source
	if len(parts) == 2 && imageHasRegistry(source) {
		if !strings.HasSuffix(parts[0], "-registry.local") {
			return name.Tag{}, fmt.Errorf("podplane push sources must come from Podplane's local apps/ registry cache; use a tag like api:v1 or default-registry.local/apps/api:v1")
		}
		pathTag = parts[1]
		if !strings.HasPrefix(pathTag, "apps/") {
			return name.Tag{}, fmt.Errorf("podplane push sources with an explicit local registry host must be under apps/; use %s/apps/%s", parts[0], pathTag)
		}
	}
	repo, tag := splitRepoTag(pathTag)
	if repo == "" {
		return name.Tag{}, fmt.Errorf("image name cannot be empty")
	}
	if tag == "" {
		return name.Tag{}, fmt.Errorf("image tag cannot be empty")
	}
	if strings.HasPrefix(repo, "mirror/") || repo == "mirror" {
		return name.Tag{}, fmt.Errorf("podplane push cannot read from mirror/; mirror/ is reserved for Podplane-managed dependency mirrors")
	}
	if !strings.HasPrefix(repo, "apps/") {
		if strings.Contains(repo, "/") {
			return name.Tag{}, fmt.Errorf("podplane push sources must be under apps/; use apps/%s:%s", path.Base(repo), tag)
		}
		repo = "apps/" + repo
	}
	return name.NewTag(repo+":"+tag, name.WeakValidation)
}

// remoteRef resolves and validates the target registry image reference.
func remoteRef(registryHost string, source name.Reference, remoteImage string) (name.Reference, error) {
	if strings.Contains(remoteImage, "@") {
		return nil, fmt.Errorf("remote image must use NAME[:TAG], not a digest reference")
	}
	if remoteImage == "" {
		repo := source.Context().RepositoryStr()
		if !strings.HasPrefix(repo, "apps/") {
			if i := strings.LastIndex(repo, "/"); i >= 0 {
				repo = repo[i+1:]
			}
			repo = "apps/" + repo
		}
		remoteImage = registryHost + "/" + repo + ":" + source.Identifier()
	} else if !imageHasRegistry(remoteImage) {
		remoteImage = registryHost + "/" + strings.TrimPrefix(remoteImage, "/")
	}
	ref, err := name.ParseReference(remoteImage, name.WeakValidation)
	if err != nil {
		return nil, fmt.Errorf("parse remote image %q: %w", remoteImage, err)
	}
	repo := ref.Context().RepositoryStr()
	if !strings.HasPrefix(repo, "apps/") && repo != "apps" {
		return nil, fmt.Errorf("remote image %q must be under apps/**; mirror/** is reserved for Podplane-managed dependency images", ref.Name())
	}
	return ref, nil
}

// imageHasRegistry reports whether an image reference starts with an explicit registry host.
func imageHasRegistry(ref string) bool {
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return false
	}
	first := ref[:slash]
	return strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost"
}

// splitRepoTag splits an image path into repository and tag components.
func splitRepoTag(pathTag string) (string, string) {
	slash := strings.LastIndex(pathTag, "/")
	colon := strings.LastIndex(pathTag, ":")
	if colon > slash {
		return pathTag[:colon], pathTag[colon+1:]
	}
	return pathTag, "latest"
}
