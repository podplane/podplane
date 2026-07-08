// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
	"gopkg.in/yaml.v3"
)

const (
	podplaneComponentsGitKey         = "/registry/source.toolkit.fluxcd.io/gitrepositories/platform-components/podplane-components"
	platformComponentsHelmReleaseKey = "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components"
)

type SnapshotOptions struct {
	Context             context.Context
	ClusterConfigPath   string
	SeedPath            string
	ValuesFile          string
	ZotRegistryEndpoint string
}

// WriteSnapshot seeds a Netsy snapshot with platform-components values derived
// from the cluster config interpolated into the seed file.
func WriteSnapshot(w io.Writer, opts SnapshotOptions) error {
	if opts.SeedPath == "" {
		return fmt.Errorf("seed path is required")
	}
	cluster, err := clusterconfig.Load(opts.ClusterConfigPath)
	if err != nil {
		return err
	}
	values, err := buildPlatformComponentsValues(cluster, buildPlatformComponentsValuesOptions{ZotRegistryEndpoint: opts.ZotRegistryEndpoint})
	if err != nil {
		return err
	}
	if err := mergeValuesFile(values, opts.ValuesFile); err != nil {
		return err
	}
	seedData, err := loadSeedFile(opts.Context, opts.SeedPath)
	if err != nil {
		return err
	}
	records, err := datafile.ReadSnapshot(bytes.NewReader(seedData))
	if err != nil {
		return fmt.Errorf("read Podplane seed file: %w", err)
	}
	if err := interpolatePlatformComponents(records, values); err != nil {
		return err
	}
	if err := interpolateComponentsSource(records, cluster.Cluster.Components.Source); err != nil {
		return err
	}
	if cluster.Cluster.Components.Registry != nil && cluster.Cluster.Components.Registry.Mirror.Enabled {
		if err := rewriteSeedImages(records, cluster.Cluster.RegistryMirrorHostname(), cluster.Cluster.RegistryMirrorPrefix()); err != nil {
			return err
		}
	}
	if err := datafile.WriteSnapshot(w, records, cluster.Cluster.ID); err != nil {
		return fmt.Errorf("write Netsy snapshot: %w", err)
	}
	return nil
}

// mergeValuesFile merges a YAML/JSON values file over dst.
func mergeValuesFile(dst map[string]any, path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read values file %s: %w", path, err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("decode values file %s: %w", path, err)
	}
	if values != nil {
		deepMerge(dst, values)
	}
	return nil
}

// loadSeedFile returns the Podplane seed file from a local path or URL.
func loadSeedFile(ctx context.Context, seed string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(seed)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		data, err := os.ReadFile(seed)
		if err != nil {
			return nil, fmt.Errorf("read Podplane seed file %s: %w", seed, err)
		}
		return data, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, seed, nil)
	if err != nil {
		return nil, fmt.Errorf("download Podplane seed file %s: %w", seed, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Podplane seed file %s: %w", seed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download Podplane seed file %s: HTTP %s", seed, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read Podplane seed file response: %w", err)
	}
	return data, nil
}

// interpolatePlatformComponents merges derived platform-components values into
// the platform-components HelmRelease record in a Netsy snapshot.
func interpolatePlatformComponents(records []*datafile.Record, values map[string]any) error {
	for i := range records {
		if string(records[i].Key) != platformComponentsHelmReleaseKey {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(records[i].Value, &obj); err != nil {
			return fmt.Errorf("decode platform-components HelmRelease at %s: %w", platformComponentsHelmReleaseKey, err)
		}
		if obj["kind"] != "HelmRelease" || !strings.HasPrefix(stringValue(obj["apiVersion"]), "helm.toolkit.fluxcd.io/") {
			return fmt.Errorf("record at %s is not a Flux HelmRelease", platformComponentsHelmReleaseKey)
		}
		metadata, _ := obj["metadata"].(map[string]any)
		if metadata["name"] != "platform-components" {
			return fmt.Errorf("record at %s is not the platform-components HelmRelease", platformComponentsHelmReleaseKey)
		}
		if namespace := stringValue(metadata["namespace"]); namespace != "" && namespace != "platform-components" {
			return fmt.Errorf("record at %s is in namespace %q, want platform-components", platformComponentsHelmReleaseKey, namespace)
		}
		spec := ensureMap(obj, "spec")
		specValues := ensureMap(spec, "values")
		deepMerge(specValues, values)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(obj); err != nil {
			return fmt.Errorf("encode platform-components HelmRelease: %w", err)
		}
		records[i].Value = buf.Bytes()
		return nil
	}
	return fmt.Errorf("podplane seed file does not contain the platform-components HelmRelease at %s", platformComponentsHelmReleaseKey)
}

// rewriteSeedImages prefixes JSON image fields with the configured registry
// mirror host and path prefix. Seedgen is responsible for normalizing image
// references before Podplane receives the seed.
func rewriteSeedImages(records []*datafile.Record, mirrorHostname, mirrorPrefix string) error {
	mirrorBaseURL := strings.TrimSuffix(mirrorHostname, "/")
	if mirrorBaseURL == "" {
		return nil
	}
	mirrorPrefix = strings.Trim(strings.TrimSpace(mirrorPrefix), "/")
	if mirrorPrefix != "" {
		mirrorBaseURL += "/" + mirrorPrefix
	}
	for i := range records {
		var obj any
		if err := json.Unmarshal(records[i].Value, &obj); err != nil {
			continue
		}
		if !rewriteImageFields(obj, mirrorBaseURL) {
			continue
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(obj); err != nil {
			return fmt.Errorf("encode image-rewritten seed record %s: %w", records[i].Key, err)
		}
		records[i].Value = bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	}
	return nil
}

// rewriteImageFields recursively prefixes string fields named image with the
// configured mirror base url.
func rewriteImageFields(value any, mirrorBaseURL string) bool {
	var changed bool
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "image" {
				image, ok := child.(string)
				if ok && image != "" && !strings.HasPrefix(image, mirrorBaseURL+"/") {
					v[key] = mirrorBaseURL + "/" + image
					changed = true
				}
				continue
			}
			if rewriteImageFields(child, mirrorBaseURL) {
				changed = true
			}
		}
	case []any:
		for _, child := range v {
			if rewriteImageFields(child, mirrorBaseURL) {
				changed = true
			}
		}
	}
	return changed
}

// interpolateComponentsSource updates the bootstrap GitRepository used by Flux
// to source the platform-components chart and child component HelmReleases.
func interpolateComponentsSource(records []*datafile.Record, source *clusterconfig.ComponentsSource) error {
	if source == nil || source.URL == "" {
		return nil
	}
	for i := range records {
		if string(records[i].Key) != podplaneComponentsGitKey {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(records[i].Value, &obj); err != nil {
			return fmt.Errorf("decode podplane-components GitRepository at %s: %w", podplaneComponentsGitKey, err)
		}
		if obj["kind"] != "GitRepository" || !strings.HasPrefix(stringValue(obj["apiVersion"]), "source.toolkit.fluxcd.io/") {
			return fmt.Errorf("record at %s is not a Flux GitRepository", podplaneComponentsGitKey)
		}
		metadata, _ := obj["metadata"].(map[string]any)
		if metadata["name"] != "podplane-components" {
			return fmt.Errorf("record at %s is not the podplane-components GitRepository", podplaneComponentsGitKey)
		}
		if namespace := stringValue(metadata["namespace"]); namespace != "" && namespace != "platform-components" {
			return fmt.Errorf("record at %s is in namespace %q, want platform-components", podplaneComponentsGitKey, namespace)
		}
		spec := ensureMap(obj, "spec")
		spec["url"] = source.URL
		if source.SecretRef != nil && source.SecretRef.Name != "" {
			spec["secretRef"] = map[string]any{"name": source.SecretRef.Name}
		} else {
			delete(spec, "secretRef")
		}
		ref := map[string]any{}
		if source.Ref.Branch != "" {
			ref["branch"] = source.Ref.Branch
		}
		if source.Ref.Tag != "" {
			ref["tag"] = source.Ref.Tag
		}
		if source.Ref.Semver != "" {
			ref["semver"] = source.Ref.Semver
		}
		if source.Ref.Commit != "" {
			ref["commit"] = source.Ref.Commit
		}
		if len(ref) > 0 {
			spec["ref"] = ref
		} else {
			delete(spec, "ref")
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(obj); err != nil {
			return fmt.Errorf("encode podplane-components GitRepository: %w", err)
		}
		records[i].Value = buf.Bytes()
		return nil
	}
	return fmt.Errorf("podplane seed file does not contain the podplane-components GitRepository at %s", podplaneComponentsGitKey)
}

// ensureMap returns the existing map value for key or creates and stores a new
// map when the key is absent or not already a map.
func ensureMap(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// deepMerge recursively merges src into dst, preserving existing nested maps
// and replacing non-map values.
func deepMerge(dst, src map[string]any) {
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = value
	}
}

// stringValue returns value when it is a string, otherwise returning the empty
// string.
func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
