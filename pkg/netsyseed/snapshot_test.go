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

// TestInterpolatePlatformComponentsMergesValues checks derived app values preserve existing HelmRelease values.
func TestInterpolatePlatformComponentsMergesValues(t *testing.T) {
	records := []*datafile.Record{
		{Key: []byte("/registry/configmaps/default/ignored"), Value: []byte(`{"kind":"ConfigMap","metadata":{"name":"ignored"}}`)},
		{Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{"platform":{"components":{"apps":{"cilium":{"enabled":true}}}}}}}`)},
	}
	values := map[string]any{
		"platform": map[string]any{
			"components": map[string]any{
				"apps": map[string]any{
					"envoy-gateway": map[string]any{"enabled": true},
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
	if apps["envoy-gateway"] == nil {
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

// TestInterpolateKubernetesAcceptsStrippedIPAddress verifies interpolation
// accepts Services whose generated IPAddress state was stripped by seedgen;
// kube-apiserver's startup allocator repair reconstructs those records.
func TestInterpolateKubernetesAcceptsStrippedIPAddress(t *testing.T) {
	network, err := clusterconfig.ServiceNetworkFromCIDRs([]string{"10.96.0.0/12"})
	if err != nil {
		t.Fatal(err)
	}
	records := []*datafile.Record{
		{Key: []byte(serviceCIDRKey), Value: []byte(`{"kind":"ServiceCIDR","spec":{"cidrs":["198.18.0.0/15"]}}`)},
		{Key: []byte("/registry/services/specs/platform-example/example"), Value: []byte(`{"kind":"Service","metadata":{"name":"example","namespace":"platform-example"},"spec":{"clusterIP":"198.18.0.2","clusterIPs":["198.18.0.2"],"ipFamilies":["IPv4"],"ipFamilyPolicy":"SingleStack"}}`)},
	}

	got, err := interpolateKubernetes(records, network)
	if err != nil {
		t.Fatalf("interpolateKubernetes error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(got))
	}
	var service map[string]any
	if err := json.Unmarshal(got[1].Value, &service); err != nil {
		t.Fatal(err)
	}
	spec := service["spec"].(map[string]any)
	if gotIP, wantIP := spec["clusterIP"], "10.96.0.2"; gotIP != wantIP {
		t.Fatalf("clusterIP = %v, want %s", gotIP, wantIP)
	}
}

// TestInterpolateComponentsSourceUpdatesGitRepository checks source URL, ref, and credentials replace seed defaults.
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

// TestInterpolateComponentsSourceRemovesSecretRefWhenUnset checks stale credentials are removed and semver is applied.
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

// TestRewriteSeedImagesPrefixesAllJSONImageFields checks container images are mirrored without changing non-JSON records.
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

// TestRewriteImageFields checks recursive image rewriting leaves empty, mirrored, and unrelated values unchanged.
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

// containerImage extracts an image from an indexed container list in a decoded pod spec.
func containerImage(t *testing.T, podSpec map[string]any, field string, index int) string {
	t.Helper()
	items := podSpec[field].([]any)
	container := items[index].(map[string]any)
	return container["image"].(string)
}

// TestMergeValuesPrecedence verifies JSON content is accepted, files win on
// conflicts, and nested non-conflicting content values survive.
func TestMergeValuesPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(path, []byte(`
platform:
  components:
    imageMirror:
      enabled: true
      hostname: first.example.com
    apps:
      envoy-gateway:
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

	content := []byte(`{"platform":{"components":{"imageMirror":{"enabled":false,"prefix":"content"},"apps":{"coredns":{"enabled":true}}}}}`)
	if err := mergeValuesContent(values, content, "test content"); err != nil {
		t.Fatalf("mergeValuesContent error = %v", err)
	}
	if err := mergeValuesFile(values, path); err != nil {
		t.Fatalf("mergeValuesFile error = %v", err)
	}
	components := values["platform"].(map[string]any)["components"].(map[string]any)
	mirror := components["imageMirror"].(map[string]any)
	if got, want := mirror["enabled"], true; got != want {
		t.Fatalf("mirror.enabled = %v, want %v", got, want)
	}
	if got, want := mirror["prefix"], "content"; got != want {
		t.Fatalf("mirror.prefix = %v, want %v", got, want)
	}
	if got, want := mirror["hostname"], "first.example.com"; got != want {
		t.Fatalf("mirror.hostname = %v, want %v", got, want)
	}
	apps := components["apps"].(map[string]any)
	if apps["cilium"] == nil || apps["coredns"] == nil || apps["envoy-gateway"] == nil {
		t.Fatalf("apps were not merged: %v", apps)
	}
}

// TestWriteSnapshotNodeCertificates checks local and tunnel delivery and prevents
// captured seed configuration or overrides from re-enabling ingress issuance.
func TestWriteSnapshotNodeCertificates(t *testing.T) {
	for _, mode := range []string{"local", "tunnel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			cfg, err := clusterconfig.NewDraftConfig("aws")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Cluster.Domains = []clusterconfig.Domain{{Zone: "example.com"}, {Zone: "second.example.net"}}
			switch mode {
			case "tunnel":
				cfg.Cluster.Tunnel = &clusterconfig.Tunnel{
					Kind: "cloudflare",
					ID:   "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8", CredentialSecret: "tunnel-credentials",
				}
			case "local":
				cfg.Cluster.Providers = nil
			}
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "cluster.jsonc")
			if err := os.WriteFile(configPath, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			records := []*datafile.Record{
				{Revision: 1, Key: []byte("_netsy"), Value: []byte(`{}`)},
				{Revision: 2, Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components"},"spec":{"values":{"platform":{"components":{"values":{"podplane-operator":{"podplane":{"operator":{"config":{"ingressCertificates":{"domains":{"old.example":{}}},"workloadCertificates":{"caPath":"/workload/key"}}}}}}}}}}}`)},
				{Revision: 3, Key: []byte(serviceCIDRKey), Value: []byte(`{"kind":"ServiceCIDR","spec":{"cidrs":["198.18.0.0/15"]}}`)},
				{Revision: 4, Key: []byte(envoyGatewayKey), Value: []byte(`{"kind":"Gateway","spec":{"listeners":[{"name":"http","protocol":"HTTP","port":80}]}}`)},
				{Revision: 5, Key: []byte(envoyProxyKey), Value: []byte(`{"kind":"EnvoyProxy","spec":{"provider":{"kubernetes":{"envoyDaemonSet":{"patch":{"value":{"spec":{"template":{"spec":{"containers":[{"name":"envoy"},{"name":"podplane-sds","args":["sds","--certificates-dir=/captured/certificates","--certificate=bundle-old=old.example"],"volumeMounts":[{"name":"ingress-certificates","mountPath":"/captured/certificates","readOnly":true}]}],"volumes":[{"name":"ingress-certificates","csi":{"driver":"secrets-store.csi.k8s.io"}}]}}}}}}}}}}`)},
				{Revision: 6, Key: []byte(envoySPCKey), Value: []byte(`{"kind":"SecretProviderClass","spec":{"provider":"aws"}}`)},
				{Revision: 7, Key: []byte("/registry/configmaps/platform-podplane-operator/platform-podplane-operator-config"), Value: []byte(`{"kind":"ConfigMap","data":{"config.json":"{\"ingress_certificates\":{\"domains\":{\"old.example\":{}}},\"certificates\":{\"ca_path\":\"/workload/key\"}}"}}`)},
				{Revision: 8, Key: []byte("/registry/helm.toolkit.fluxcd.io/helmreleases/platform-podplane-operator/podplane-operator"), Value: []byte(`{"kind":"HelmRelease","spec":{"values":{"podplane":{"operator":{"config":{"ingressCertificates":{"domains":{"old.example":{}}}}}}}}}`)},
			}
			var seed bytes.Buffer
			if err := datafile.WriteSnapshot(&seed, records, cfg.Cluster.ID); err != nil {
				t.Fatal(err)
			}
			seedPath := filepath.Join(dir, "seed.netsy")
			if err := os.WriteFile(seedPath, seed.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := WriteSnapshot(&output, SnapshotOptions{
				ClusterConfigPath: configPath, SeedPath: seedPath,
				NodeIngressCertificate: mode == "local",
				ValuesContent:          []byte(`{"platform":{"components":{"values":{"podplane-operator":{"podplane":{"operator":{"config":{"ingressCertificates":{"domains":{"override.example":{}}}}}}},"envoy-gateway":{"platform":{"envoyGateway":{"ingress":{"certificates":{"provider":"aws"}}}}}}}}}`),
			}); err != nil {
				t.Fatal(err)
			}
			got, err := datafile.ReadSnapshot(bytes.NewReader(output.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if string(got[0].Key) != "_netsy" {
				t.Fatal("bootstrap snapshot lost Netsy's initial record")
			}
			secretCount := 0
			for i, record := range got {
				if record.Revision != int64(i+1) {
					t.Fatalf("bootstrap record %s has revision %d, want %d", record.Key, record.Revision, i+1)
				}
				obj := decodeTestObject(t, record)
				switch string(record.Key) {
				case platformComponentsHelmReleaseKey:
					values := obj["spec"].(map[string]any)["values"].(map[string]any)
					config := componentValues(values, "podplane-operator")["podplane"].(map[string]any)["operator"].(map[string]any)["config"].(map[string]any)
					if config["ingressCertificates"] != nil || config["workloadCertificates"] == nil {
						t.Fatalf("issuer/workload configuration = %v", config)
					}
					ingress := componentValues(values, "envoy-gateway")["platform"].(map[string]any)["envoyGateway"].(map[string]any)["ingress"].(map[string]any)
					if ingress["certificates"].(map[string]any)["provider"] != "node" || len(ingress["domains"].([]any)) != 2 {
						t.Fatalf("tunnel ingress values = %v", ingress)
					}
				case envoyGatewayKey:
					listeners := obj["spec"].(map[string]any)["listeners"].([]any)
					if len(listeners) != 5 {
						t.Fatalf("listeners = %v", listeners)
					}
					for _, value := range listeners[1:] {
						listener := value.(map[string]any)
						ref := listener["tls"].(map[string]any)["certificateRefs"].([]any)[0].(map[string]any)
						if ref["name"] != "node" {
							t.Fatalf("certificate reference = %v", ref)
						}
					}
				case envoyProxyKey:
					containers, err := nestedSlice(obj, "test", "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec", "containers")
					if err != nil {
						t.Fatal(err)
					}
					args := containers[1].(map[string]any)["args"].([]any)
					if len(args) != 2 || args[1] != "--node-certificate=example.com,second.example.net" {
						t.Fatalf("SDS args = %v", args)
					}
					mount := containers[1].(map[string]any)["volumeMounts"].([]any)[0].(map[string]any)
					if mount["mountPath"] != "/var/run/podplane/ingress-certificates" || mount["readOnly"] != true {
						t.Fatalf("SDS mount = %v", mount)
					}
					volumes, err := nestedSlice(obj, "test", "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec", "volumes")
					if err != nil {
						t.Fatal(err)
					}
					volume := volumes[0].(map[string]any)
					if volume["csi"] != nil || volume["hostPath"].(map[string]any)["path"] != "/opt/podplane/secrets/ingress" {
						t.Fatalf("ingress volume = %v", volume)
					}
				case envoySPCKey:
					t.Fatal("tunnel snapshot contains ingress CSI configuration")
				case "/registry/secrets/platform-envoy-gateway/node":
					secretCount++
					if obj["type"] != "gateway.envoyproxy.io/sds" || len(obj["data"].(map[string]any)) != 2 {
						t.Fatalf("SDS pointer = %v", obj)
					}
				case "/registry/configmaps/platform-podplane-operator/platform-podplane-operator-config":
					var config map[string]any
					if err := json.Unmarshal([]byte(obj["data"].(map[string]any)["config.json"].(string)), &config); err != nil {
						t.Fatal(err)
					}
					if config["ingress_certificates"] != nil || config["certificates"] == nil {
						t.Fatalf("seeded operator configuration = %v", config)
					}
				case "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-podplane-operator/podplane-operator":
					config := obj["spec"].(map[string]any)["values"].(map[string]any)["podplane"].(map[string]any)["operator"].(map[string]any)["config"].(map[string]any)
					if config["ingressCertificates"] != nil {
						t.Fatalf("captured operator Helm values = %v", config)
					}
				}
			}
			if secretCount != 1 {
				t.Fatalf("shared SDS pointers = %d, want 1", secretCount)
			}
		})
	}
}

// TestWriteSnapshotNLBFromNodeSeed verifies a local node-certificate capture
// can bootstrap NLB ingress with operator-managed fallback and optional ACME.
func TestWriteSnapshotNLBFromNodeSeed(t *testing.T) {
	for _, provider := range []string{"aws", "gcp"} {
		for _, mode := range []string{"fallback", "acme"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				cfg, err := clusterconfig.NewDraftConfig("aws")
				if err != nil {
					t.Fatal(err)
				}
				if provider == "gcp" {
					cfg.Cluster.Secrets = clusterconfig.Secrets{
						DefaultProvider: "google",
						Providers:       map[string]clusterconfig.SecretsProvider{"google": {Kind: "gcp", ProjectID: "test-project", KeyPrefix: "target"}},
					}
				}
				if mode == "acme" {
					cfg.Cluster.ACME = &clusterconfig.ACME{Email: "admin@example.com"}
				}
				cfg.Cluster.Domains = []clusterconfig.Domain{{Zone: "example.com", Provider: &clusterconfig.DomainProvider{Kind: "aws-route53", Region: "us-east-1"}}}
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				configPath := filepath.Join(dir, "cluster.jsonc")
				if err := os.WriteFile(configPath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				records := []*datafile.Record{
					{Revision: 1, Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components"},"spec":{"values":{}}}`)},
					{Revision: 2, Key: []byte(serviceCIDRKey), Value: []byte(`{"kind":"ServiceCIDR","spec":{"cidrs":["198.18.0.0/15"]}}`)},
					{Revision: 3, Key: []byte(envoyGatewayKey), Value: []byte(`{"kind":"Gateway","spec":{"listeners":[{"name":"https-old","protocol":"HTTPS","hostname":"default.localhost","tls":{"certificateRefs":[{"name":"node"}]}}]}}`)},
					{Revision: 4, Key: []byte(envoyProxyKey), Value: []byte(`{"kind":"EnvoyProxy","spec":{"provider":{"kubernetes":{"envoyDaemonSet":{"patch":{"value":{"spec":{"template":{"spec":{"containers":[{"name":"podplane-sds","args":["sds","--node-certificate=default.localhost"],"volumeMounts":[{"name":"ingress-certificates","mountPath":"/var/run/podplane/ingress-certificates","readOnly":true}]}],"volumes":[{"name":"ingress-certificates","hostPath":{"path":"/opt/podplane/secrets/ingress","type":"Directory"}}]}}}}}}}}}}`)},
				}
				var seed bytes.Buffer
				if err := datafile.WriteSnapshot(&seed, records, cfg.Cluster.ID); err != nil {
					t.Fatal(err)
				}
				seedPath := filepath.Join(dir, "seed.netsy")
				if err := os.WriteFile(seedPath, seed.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				if err := WriteSnapshot(&output, SnapshotOptions{ClusterConfigPath: configPath, SeedPath: seedPath}); err != nil {
					t.Fatal(err)
				}
				got, err := datafile.ReadSnapshot(bytes.NewReader(output.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				objects := map[string]map[string]any{}
				for i, record := range got {
					if record.Revision != int64(i+1) {
						t.Fatalf("noncontiguous revision: %d", record.Revision)
					}
					objects[string(record.Key)] = decodeTestObject(t, record)
				}
				values := objects[platformComponentsHelmReleaseKey]["spec"].(map[string]any)["values"].(map[string]any)
				config := componentValues(values, "podplane-operator")["podplane"].(map[string]any)["operator"].(map[string]any)["config"].(map[string]any)
				issuer, ok := config["ingressCertificates"].(map[string]any)
				if !ok || issuer["domains"].(map[string]any)["example.com"] == nil {
					t.Fatalf("missing NLB ingress issuer: %v", config)
				}
				if mode == "acme" {
					if issuer["acme"].(map[string]any)["email"] != "admin@example.com" {
						t.Fatalf("ACME issuer = %v", issuer)
					}
				} else if issuer["acme"] != nil {
					t.Fatalf("fallback-only issuer contains ACME: %v", issuer)
				}
				proxy := objects[envoyProxyKey]
				volumes, err := nestedSlice(proxy, "test", "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec", "volumes")
				if err != nil {
					t.Fatal(err)
				}
				volume := volumes[0].(map[string]any)
				if volume["hostPath"] != nil {
					t.Fatalf("NLB still uses node certificate volume: %v", volume)
				}
				csi := volume["csi"].(map[string]any)
				if csi["driver"] != "secrets-store.csi.k8s.io" || csi["readOnly"] != true || csi["volumeAttributes"].(map[string]any)["secretProviderClass"] != "platform-envoy-gateway-ingress-certificates" {
					t.Fatalf("CSI volume = %v", csi)
				}
				spc := objects[envoySPCKey]
				if spc == nil || spc["spec"].(map[string]any)["provider"] != provider {
					t.Fatalf("NLB SecretProviderClass = %v", spc)
				}
				parameters := spc["spec"].(map[string]any)["parameters"].(map[string]any)
				if provider == "gcp" {
					if parameters["objects"] != nil || !strings.Contains(stringValue(parameters["secrets"]), "projects/test-project/secrets/target_platform-cluster_ingress-certificates_bundle-a379a6f6eeafb9a55e378c11/versions/latest") {
						t.Fatalf("GCP SecretProviderClass parameters = %v", parameters)
					}
				} else if !strings.Contains(stringValue(parameters["objects"]), `objectType: "secretsmanager"`) {
					t.Fatalf("AWS SecretProviderClass parameters = %v", parameters)
				}
				metadata := spc["metadata"].(map[string]any)
				if metadata["name"] != "platform-envoy-gateway-ingress-certificates" || metadata["namespace"] != "platform-envoy-gateway" || metadata["annotations"].(map[string]any)["meta.helm.sh/release-name"] != "platform-envoy-gateway" {
					t.Fatalf("SecretProviderClass metadata = %v", metadata)
				}
				if objects["/registry/secrets/platform-envoy-gateway/bundle-a379a6f6eeafb9a55e378c11"] == nil || objects["/registry/secrets/platform-envoy-gateway/node"] != nil {
					t.Fatal("NLB snapshot must contain domain SDS references, not a node reference")
				}
				containers, err := nestedSlice(proxy, "test", "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec", "containers")
				if err != nil {
					t.Fatal(err)
				}
				args := containers[0].(map[string]any)["args"].([]any)
				if len(args) != 2 || args[1] != "--domain-certificate=bundle-a379a6f6eeafb9a55e378c11=example.com" {
					t.Fatalf("NLB SDS args = %v", args)
				}
			})
		}
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
		Secrets: clusterconfig.Secrets{DefaultProvider: "local-fakevault", Providers: map[string]clusterconfig.SecretsProvider{
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

	// Verify the interpolated values contain our seeded gateway domain.
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
			envoyGateway := components["values"].(map[string]any)["envoy-gateway"].(map[string]any)
			zone := envoyGateway["platform"].(map[string]any)["envoyGateway"].(map[string]any)["ingress"].(map[string]any)["domains"].([]any)[0].(map[string]any)["apex"]
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
