// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
)

func TestInterpolatePlatformComponentsMergesValues(t *testing.T) {
	records := []*datafile.Record{
		{Key: []byte("/registry/configmaps/default/ignored"), Value: []byte(`{"kind":"ConfigMap","metadata":{"name":"ignored"}}`)},
		{Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{"platform":{"components":{"apps":{"cilium":{"enabled":true}}}}}}}`)},
	}
	values := map[string]any{
		"platform": map[string]any{
			"components": map[string]any{
				"apps": map[string]any{
					"traefik": map[string]any{"enabled": true},
				},
			},
		},
	}

	if err := interpolatePlatformComponents(records, values); err != nil {
		t.Fatalf("interpolatePlatformComponents error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(records[1].Value, &got); err != nil {
		t.Fatalf("unmarshal interpolated HelmRelease: %v", err)
	}
	apps := got["spec"].(map[string]any)["values"].(map[string]any)["platform"].(map[string]any)["components"].(map[string]any)["apps"].(map[string]any)
	if apps["cilium"] == nil {
		t.Fatalf("existing app values were not preserved")
	}
	if apps["traefik"] == nil {
		t.Fatalf("derived app values were not merged")
	}
}

// TestInterpolateKubernetesIPv4Only verifies conversion of a dual-stack seed to IPv4.
func TestInterpolateKubernetesIPv4Only(t *testing.T) {
	network, err := clusterconfig.ServiceNetworkFromCIDRs([]string{"10.96.0.0/12"})
	if err != nil {
		t.Fatal(err)
	}
	records := []*datafile.Record{
		{Key: []byte(serviceCIDRKey), Value: []byte(`{"kind":"ServiceCIDR","spec":{"cidrs":["198.18.0.0/15","fdc6::/108"]}}`)},
		{Key: []byte(apiServiceKey), Value: []byte(`{"kind":"Service","metadata":{"name":"kubernetes","namespace":"default"},"spec":{"clusterIP":"198.18.0.1","clusterIPs":["198.18.0.1","fdc6::1"],"ipFamilies":["IPv4","IPv6"],"ipFamilyPolicy":"PreferDualStack","ports":[{"port":443,"targetPort":6443}]}}`)},
		{Key: []byte(coreDNSServiceKey), Value: []byte(`{"kind":"Service","metadata":{"name":"platform-coredns","namespace":"platform-coredns"},"spec":{"clusterIP":"198.19.255.254","clusterIPs":["198.19.255.254","fdc6::ffff"],"ipFamilies":["IPv4","IPv6"],"ipFamilyPolicy":"PreferDualStack"}}`)},
		{Key: []byte("/registry/ipaddresses/198.18.0.15"), Value: []byte(`{"kind":"IPAddress","metadata":{"name":"198.18.0.15","labels":{"ipaddress.kubernetes.io/ip-family":"IPv4"}},"spec":{"parentRef":{"namespace":"platform-example","name":"example"}}}`)},
		{Key: []byte("/registry/services/specs/platform-example/example"), Value: []byte(`{"kind":"Service","metadata":{"name":"example","namespace":"platform-example"},"spec":{"clusterIP":"198.18.0.15","clusterIPs":["198.18.0.15"],"ipFamilies":["IPv4","IPv6"],"ipFamilyPolicy":"PreferDualStack"}}`)},
		{Key: []byte("/registry/ipaddresses/198.18.0.1"), Value: []byte(`{"kind":"IPAddress","metadata":{"name":"198.18.0.1","labels":{"ipaddress.kubernetes.io/ip-family":"IPv4"}},"spec":{"parentRef":{"namespace":"default","name":"kubernetes"}}}`)},
		{Key: []byte("/registry/ipaddresses/198.19.255.254"), Value: []byte(`{"kind":"IPAddress","metadata":{"name":"198.19.255.254","labels":{"ipaddress.kubernetes.io/ip-family":"IPv4"}},"spec":{"parentRef":{"namespace":"platform-coredns","name":"platform-coredns"}}}`)},
		{Key: []byte("/registry/ipaddresses/fdc6::ffff"), Value: []byte(`{"kind":"IPAddress","metadata":{"name":"fdc6::ffff","labels":{"ipaddress.kubernetes.io/ip-family":"IPv6"}},"spec":{"parentRef":{"namespace":"platform-coredns","name":"platform-coredns"}}}`)},
		{Key: []byte(coreDNSHelmReleaseKey), Value: []byte(`{"kind":"HelmRelease","spec":{"values":{"coredns":{}}}}`)},
	}
	records, err = interpolateKubernetes(records, network)
	if err != nil {
		t.Fatalf("interpolateKubernetes error = %v", err)
	}

	objects := map[string]map[string]any{}
	for _, record := range records {
		var obj map[string]any
		if err := json.Unmarshal(record.Value, &obj); err != nil {
			t.Fatal(err)
		}
		objects[string(record.Key)] = obj
	}
	serviceCIDRs := objects[serviceCIDRKey]["spec"].(map[string]any)["cidrs"].([]any)
	if len(serviceCIDRs) != 1 || serviceCIDRs[0] != "10.96.0.0/12" {
		t.Fatalf("ServiceCIDR cidrs = %#v", serviceCIDRs)
	}
	apiSpec := objects[apiServiceKey]["spec"].(map[string]any)
	if apiSpec["clusterIP"] != "10.96.0.1" || apiSpec["ipFamilyPolicy"] != "SingleStack" {
		t.Fatalf("API Service spec = %#v", apiSpec)
	}
	dnsSpec := objects[coreDNSServiceKey]["spec"].(map[string]any)
	if dnsSpec["clusterIP"] != "10.111.255.254" || len(dnsSpec["clusterIPs"].([]any)) != 1 {
		t.Fatalf("CoreDNS Service spec = %#v", dnsSpec)
	}
	exampleSpec := objects["/registry/services/specs/platform-example/example"]["spec"].(map[string]any)
	if exampleSpec["clusterIP"] != "10.96.0.15" || len(exampleSpec["ipFamilies"].([]any)) != 1 {
		t.Fatalf("example Service spec = %#v", exampleSpec)
	}
	for _, key := range []string{"/registry/ipaddresses/10.96.0.1", "/registry/ipaddresses/10.111.255.254", "/registry/ipaddresses/10.96.0.15"} {
		if objects[key] == nil {
			t.Fatalf("missing allocation record %s", key)
		}
	}
	if len(records) != 8 {
		t.Fatalf("len(records) = %d, want 8 after stale IPv6 allocation removal", len(records))
	}
}

// TestInterpolateKubernetesRejectsInvalidServiceIP verifies invalid snapshots fail loudly.
func TestInterpolateKubernetesRejectsInvalidServiceIP(t *testing.T) {
	network, err := clusterconfig.ServiceNetworkFromCIDRs([]string{"10.96.0.0/12"})
	if err != nil {
		t.Fatal(err)
	}
	records := []*datafile.Record{
		{Key: []byte(serviceCIDRKey), Value: []byte(`{"kind":"ServiceCIDR","spec":{"cidrs":["198.18.0.0/15"]}}`)},
		{Key: []byte("/registry/services/specs/platform-example/example"), Value: []byte(`{"kind":"Service","metadata":{"name":"example","namespace":"platform-example"},"spec":{"clusterIPs":["10.0.0.1"]}}`)},
	}
	if _, err := interpolateKubernetes(records, network); err == nil {
		t.Fatal("interpolateKubernetes succeeded with a Service IP outside the default service CIDR")
	}
}

func TestInterpolateComponentsSourceUpdatesGitRepository(t *testing.T) {
	records := []*datafile.Record{
		{Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{"kind":"HelmRelease","metadata":{"name":"ignored"}}`)},
		{Key: []byte(podplaneComponentsGitKey), Value: []byte(`{"apiVersion":"source.toolkit.fluxcd.io/v1","kind":"GitRepository","metadata":{"name":"podplane-components","namespace":"platform-components"},"spec":{"url":"https://github.com/podplane/components.git","ref":{"branch":"main"}}}`)},
	}
	source := &clusterconfig.ComponentsSource{
		URL:       "https://github.com/example/components.git",
		Ref:       clusterconfig.ComponentsSourceRef{Branch: "feature"},
		SecretRef: &clusterconfig.ComponentsSourceSecretRef{Name: "components-git-auth"},
	}

	if err := interpolateComponentsSource(records, source); err != nil {
		t.Fatalf("interpolateComponentsSource error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(records[1].Value, &got); err != nil {
		t.Fatalf("unmarshal interpolated GitRepository: %v", err)
	}
	spec := got["spec"].(map[string]any)
	if got, want := spec["url"], "https://github.com/example/components.git"; got != want {
		t.Fatalf("spec.url = %v, want %v", got, want)
	}
	ref := spec["ref"].(map[string]any)
	if got, want := ref["branch"], "feature"; got != want {
		t.Fatalf("spec.ref.branch = %v, want %v", got, want)
	}
	secretRef := spec["secretRef"].(map[string]any)
	if got, want := secretRef["name"], "components-git-auth"; got != want {
		t.Fatalf("spec.secretRef.name = %v, want %v", got, want)
	}
}

func TestInterpolateComponentsSourceRemovesSecretRefWhenUnset(t *testing.T) {
	records := []*datafile.Record{
		{Key: []byte(podplaneComponentsGitKey), Value: []byte(`{"apiVersion":"source.toolkit.fluxcd.io/v1","kind":"GitRepository","metadata":{"name":"podplane-components","namespace":"platform-components"},"spec":{"url":"https://github.com/podplane/components.git","secretRef":{"name":"stale"},"ref":{"branch":"main"}}}`)},
	}
	source := &clusterconfig.ComponentsSource{
		URL: "https://github.com/example/components.git",
		Ref: clusterconfig.ComponentsSourceRef{Semver: "v1.2.3"},
	}

	if err := interpolateComponentsSource(records, source); err != nil {
		t.Fatalf("interpolateComponentsSource error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(records[0].Value, &got); err != nil {
		t.Fatalf("unmarshal interpolated GitRepository: %v", err)
	}
	spec := got["spec"].(map[string]any)
	if _, ok := spec["secretRef"]; ok {
		t.Fatalf("spec.secretRef was not removed: %#v", spec["secretRef"])
	}
	ref := spec["ref"].(map[string]any)
	if got, want := ref["semver"], "v1.2.3"; got != want {
		t.Fatalf("spec.ref.semver = %v, want %v", got, want)
	}
}

func TestRewriteSeedImagesPrefixesAllJSONImageFields(t *testing.T) {
	records := []*datafile.Record{
		{Key: []byte("/registry/deployments/platform-flux/source-controller"), Value: []byte(`{"apiVersion":"apps/v1","kind":"Deployment","spec":{"template":{"spec":{"initContainers":[{"image":"docker.io/library/busybox:1"}],"containers":[{"image":"ghcr.io/fluxcd/source-controller:v1.5.0"}],"ephemeralContainers":[{"image":"dev-registry.local/mirror/docker.io/library/alpine:3"}]}}}}`)},
		{Key: []byte("/registry/configmaps/default/ignored"), Value: []byte(`not json`)},
	}

	if err := rewriteSeedImages(records, "dev-registry.local/", "mirror"); err != nil {
		t.Fatalf("rewriteSeedImages error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(records[0].Value, &got); err != nil {
		t.Fatalf("unmarshal rewritten record: %v", err)
	}
	podSpec := got["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if image := containerImage(t, podSpec, "initContainers", 0); image != "dev-registry.local/mirror/docker.io/library/busybox:1" {
		t.Fatalf("init image = %q, want mirrored", image)
	}
	if image := containerImage(t, podSpec, "containers", 0); image != "dev-registry.local/mirror/ghcr.io/fluxcd/source-controller:v1.5.0" {
		t.Fatalf("container image = %q, want mirrored", image)
	}
	if image := containerImage(t, podSpec, "ephemeralContainers", 0); image != "dev-registry.local/mirror/docker.io/library/alpine:3" {
		t.Fatalf("ephemeral image = %q, want already-mirrored image unchanged", image)
	}
	if string(records[1].Value) != `not json` {
		t.Fatalf("non-JSON record changed: %s", records[1].Value)
	}
}

func TestRewriteImageFields(t *testing.T) {
	obj := map[string]any{
		"image": "docker.io/library/top-level:1",
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"image": "ghcr.io/example/app:v1"},
				map[string]any{"image": "dev-registry.local/mirror/docker.io/library/alpine:3"},
				map[string]any{"image": ""},
			},
			"notImage": "ghcr.io/example/ignored:v1",
		},
	}

	if !rewriteImageFields(obj, "dev-registry.local/mirror") {
		t.Fatalf("rewriteImageFields reported no changes")
	}
	if got, want := obj["image"], "dev-registry.local/mirror/docker.io/library/top-level:1"; got != want {
		t.Fatalf("top-level image = %v, want %v", got, want)
	}
	spec := obj["spec"].(map[string]any)
	containers := spec["containers"].([]any)
	first := containers[0].(map[string]any)
	if got, want := first["image"], "dev-registry.local/mirror/ghcr.io/example/app:v1"; got != want {
		t.Fatalf("nested image = %v, want %v", got, want)
	}
	second := containers[1].(map[string]any)
	if got, want := second["image"], "dev-registry.local/mirror/docker.io/library/alpine:3"; got != want {
		t.Fatalf("already mirrored image = %v, want unchanged %v", got, want)
	}
	third := containers[2].(map[string]any)
	if got, want := third["image"], ""; got != want {
		t.Fatalf("empty image = %v, want unchanged empty", got)
	}
	if got, want := spec["notImage"], "ghcr.io/example/ignored:v1"; got != want {
		t.Fatalf("non-image field = %v, want unchanged %v", got, want)
	}
	if rewriteImageFields(obj, "dev-registry.local") {
		t.Fatalf("rewriteImageFields was not idempotent")
	}
}

func containerImage(t *testing.T, podSpec map[string]any, field string, index int) string {
	t.Helper()
	items := podSpec[field].([]any)
	container := items[index].(map[string]any)
	return container["image"].(string)
}

func TestMergeValuesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(path, []byte(`
platform:
  components:
    imageMirror:
      enabled: true
      hostname: first.example.com
    apps:
      traefik:
        enabled: true
`), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	values := map[string]any{
		"platform": map[string]any{
			"components": map[string]any{
				"apps": map[string]any{
					"cilium": map[string]any{"enabled": true},
				},
			},
		},
	}

	if err := mergeValuesFile(values, path); err != nil {
		t.Fatalf("mergeValuesFile error = %v", err)
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	mirror := components["imageMirror"].(map[string]any)
	if got, want := mirror["enabled"], true; got != want {
		t.Fatalf("mirror.enabled = %v, want %v", got, want)
	}
	if got, want := mirror["hostname"], "first.example.com"; got != want {
		t.Fatalf("mirror.hostname = %v, want %v", got, want)
	}
	apps := components["apps"].(map[string]any)
	if apps["cilium"] == nil || apps["traefik"] == nil {
		t.Fatalf("apps were not merged: %v", apps)
	}
}

// TestWriteSnapshotWritesBytes verifies WriteSnapshot writes the rendered
// snapshot bytes from an explicit seed file.
func TestWriteSnapshotWritesBytes(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID:   "local",
		OIDC: clusterconfig.OIDC{IssuerURL: "https://oidc.localhost/oidc"},
		Domains: []clusterconfig.Domain{
			{Zone: "local.localhost", Provider: &clusterconfig.DomainProvider{Kind: "local"}},
		},
		Components: clusterconfig.Components{Registry: &clusterconfig.ComponentsRegistry{
			Mirror: clusterconfig.ComponentsRegistryMirror{Enabled: true, Hostname: "dev-registry.local"},
		}},
		Secrets: clusterconfig.Secrets{Providers: map[string]clusterconfig.SecretsProvider{
			"local-fakevault": {Kind: "openbao", Address: "https://10.0.2.15:19443/vault/localdev"},
		}},
	}}
	// Patch validation by re-marshalling and using a non-reserved ID for Load().
	cfg.Cluster.ID = "localdev"
	cfgPath := filepath.Join(tmpDir, "cluster.jsonc")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal cluster cfg: %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("write cluster cfg: %v", err)
	}

	// Build a tiny seed snapshot in memory that contains the
	// platform-components HelmRelease record at the expected key. Serve it
	// over HTTP so loadSeedFile's URL path is exercised.
	records := []*datafile.Record{
		{Revision: 5, Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{"platform":{"components":{"values":{"podplane-operator":{"podplane":{"operator":{"config":{"cluster":{"oidc":{"issuerURL":"https://stale.example.com"}}}}}}}}}}}}`)},
		{Revision: 6, Key: []byte("/registry/deployments/platform-flux/source-controller"), Value: []byte(`{"apiVersion":"apps/v1","kind":"Deployment","spec":{"template":{"spec":{"containers":[{"image":"ghcr.io/fluxcd/source-controller:v1.5.0"}]}}}}`)},
	}
	var buf bytes.Buffer
	if err := datafile.WriteSnapshot(&buf, records, "localdev"); err != nil {
		t.Fatalf("write seed snapshot: %v", err)
	}
	seedBytes := buf.Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/recommended.netsy") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(seedBytes)
	}))
	defer server.Close()

	var data bytes.Buffer
	if err := WriteSnapshot(&data, SnapshotOptions{
		ClusterConfigPath: cfgPath,
		SeedPath:          server.URL + "/recommended.netsy",
	}); err != nil {
		t.Fatalf("WriteSnapshot error = %v", err)
	}
	if data.Len() == 0 {
		t.Fatalf("WriteSnapshot wrote no data")
	}

	// Verify the interpolated values contain our seeded traefik domain.
	got, err := datafile.ReadSnapshot(bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatalf("read built snapshot: %v", err)
	}
	var found bool
	for _, r := range got {
		switch string(r.Key) {
		case platformComponentsHelmReleaseKey:
			var obj map[string]any
			if err := json.Unmarshal(r.Value, &obj); err != nil {
				t.Fatalf("unmarshal HelmRelease: %v", err)
			}
			components := obj["spec"].(map[string]any)["values"].(map[string]any)["platform"].(map[string]any)["components"].(map[string]any)
			traefik := components["values"].(map[string]any)["traefik"].(map[string]any)
			zone := traefik["platform"].(map[string]any)["traefik"].(map[string]any)["ingress"].(map[string]any)["domains"].([]any)[0].(map[string]any)["zone"]
			if zone != "local.localhost" {
				t.Fatalf("seeded ingress zone = %v, want local.localhost", zone)
			}
			mirror := components["imageMirror"].(map[string]any)
			if mirror["enabled"] != true || mirror["hostname"] != "dev-registry.local" {
				t.Fatalf("imageMirror = %#v, want enabled dev-registry.local", mirror)
			}
			operator := components["values"].(map[string]any)["podplane-operator"].(map[string]any)["podplane"].(map[string]any)["operator"].(map[string]any)
			oidc := operator["config"].(map[string]any)["cluster"].(map[string]any)["oidc"].(map[string]any)
			if got, want := oidc["issuerURL"], "https://oidc.localhost/oidc"; got != want {
				t.Fatalf("seeded operator OIDC issuerURL = %v, want %v", got, want)
			}
			found = true
		case "/registry/deployments/platform-flux/source-controller":
			var obj map[string]any
			if err := json.Unmarshal(r.Value, &obj); err != nil {
				t.Fatalf("unmarshal Deployment: %v", err)
			}
			podSpec := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			if image := containerImage(t, podSpec, "containers", 0); image != "dev-registry.local/mirror/ghcr.io/fluxcd/source-controller:v1.5.0" {
				t.Fatalf("seeded Deployment image = %q, want mirrored", image)
			}
		default:
			continue
		}
	}
	if !found {
		t.Fatalf("platform-components HelmRelease not found in built snapshot")
	}
}
