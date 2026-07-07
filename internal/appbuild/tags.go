// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package appbuild

import (
	"fmt"
	"path"
	"strings"
)

// NormalizeTags converts user build tags into local store tags and display tags.
func NormalizeTags(tags []string, defaultRegistryHost string) ([]string, []string, error) {
	storeTags := make([]string, 0, len(tags))
	displayTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		storeTag, registryHost, err := NormalizeTag(tag, defaultRegistryHost)
		if err != nil {
			return nil, nil, err
		}
		storeTags = append(storeTags, storeTag)
		displayTags = append(displayTags, registryHost+"/"+storeTag)
	}
	return storeTags, displayTags, nil
}

// NormalizeTag converts one user build tag into a local apps/ store tag.
func NormalizeTag(tag string, defaultRegistryHost string) (string, string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", "", fmt.Errorf("image tag cannot be empty")
	}
	if strings.Contains(tag, "@") {
		return "", "", fmt.Errorf("podplane build tags must use NAME[:TAG], not digest references")
	}
	registryHost := defaultRegistryHost
	parts := strings.SplitN(tag, "/", 2)
	pathTag := tag
	hosted := false
	if len(parts) == 2 && looksLikeRegistryHost(parts[0]) {
		hosted = true
		registryHost = parts[0]
		pathTag = parts[1]
		if !strings.HasSuffix(registryHost, "-registry.local") {
			return "", "", fmt.Errorf("podplane build only writes to local Podplane registries under apps/; use a tag like api:v1 or %s/apps/api:v1", defaultRegistryHost)
		}
	}
	repo, tagPart := splitRepoTag(pathTag)
	if repo == "" {
		return "", "", fmt.Errorf("image name cannot be empty")
	}
	if tagPart == "" {
		return "", "", fmt.Errorf("image tag cannot be empty")
	}
	if strings.HasPrefix(repo, "mirror/") || repo == "mirror" {
		return "", "", fmt.Errorf("podplane build cannot write to mirror/; mirror/ is reserved for Podplane-managed dependency mirrors")
	}
	if strings.HasPrefix(repo, "apps/") {
		if strings.TrimPrefix(repo, "apps/") == "" {
			return "", "", fmt.Errorf("image name cannot be empty")
		}
		return repo + ":" + tagPart, registryHost, nil
	}
	if hosted {
		return "", "", fmt.Errorf("podplane build images with an explicit local registry host must be under apps/; use %s/apps/%s:%s", registryHost, repo, tagPart)
	}
	if strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("podplane build images must be under apps/; use apps/%s:%s", path.Base(repo), tagPart)
	}
	return "apps/" + repo + ":" + tagPart, registryHost, nil
}

// DeployNameFromImage returns the default app deployment name for an image ref.
func DeployNameFromImage(ref string) string {
	ref = strings.TrimPrefix(ref, "http://")
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimSuffix(ref, ":latest")
	if ref == "" {
		return "app"
	}
	name := path.Base(ref)
	if idx := strings.LastIndex(name, ":"); idx > 0 {
		name = name[:idx]
	}
	return name
}

// RegistryClusterID returns the local cluster ID implied by an image ref host.
func RegistryClusterID(ref string) string {
	host, _, ok := strings.Cut(ref, "/")
	if !ok {
		return "default"
	}
	return strings.TrimSuffix(host, "-registry.local")
}

// looksLikeRegistryHost reports whether s has registry-host syntax.
func looksLikeRegistryHost(s string) bool {
	return s == "localhost" || strings.ContainsAny(s, ".:")
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
