// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/netsy-dev/netsy/pkg/datafile"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/pkg/netsyseed"
)

const platformComponentsHelmReleaseKey = "/registry/helm.toolkit.fluxcd.io/helmreleases/platform-components/platform-components"

// TestPrepareComponentValues checks that provisioning and snapshot writing share
// the same merged values without changing seed membership or injecting chart-owned
// trust bundle sync defaults.
func TestPrepareComponentValues(t *testing.T) {
	dir := t.TempDir()
	configPath, seedPath := filepath.Join(dir, "cluster.jsonc"), filepath.Join(dir, "seed.netsy")
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "target", Seed: clusterconfig.Seed{Name: "agentic", Version: "v1.0.0", Digest: "sha512:" + strings.Repeat("a", 128)},
		OIDC:   clusterconfig.OIDC{IssuerURL: "https://oidc.example.com"},
		SPIFFE: clusterconfig.SPIFFE{TrustDomain: "k8s.example.com"},
		Secrets: clusterconfig.Secrets{DefaultProvider: "target-provider", Providers: map[string]clusterconfig.SecretsProvider{
			"target-provider": {Kind: "aws", ObjectType: "secretsmanager"},
		}},
		Components: clusterconfig.Components{Registry: &clusterconfig.ComponentsRegistry{
			Mirror: clusterconfig.ComponentsRegistryMirror{Enabled: true, Hostname: "registry.example.com", Prefix: "mirror"},
		}},
	}}
	if err := clusterconfig.Write(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	valuesPath := filepath.Join(dir, "values.json")
	if err := os.WriteFile(valuesPath, []byte(`{"platform":{"components":{"values":{
		"podplane-operator":{"podplane":{"operator":{"config":{
			"secrets":{"providers":{"target-provider":{"region":"ap-southeast-2"}}},
			"workloadCertificates":{"trustBundleSync":{"resources":[
				{"kind":"Secret","namespace":"custom-router","name":"extension-ca","key":"tls.crt","createIfMissing":false},
				{"kind":"ConfigMap","namespace":"custom-router","name":"shared-ca","key":"bundle.pem"}
			]}}
		}}}},
		"agent-router":{"gateway-helm":{"config":{"envoyGateway":{"extensionManager":{"service":{
			"fqdn":{"hostname":"override.example.com"},
			"tls":{"certificateRef":{"name":"extension-ca"}}
		}}}}}}
	}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		records := []*datafile.Record{
			{Revision: 1, Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(fmt.Sprintf(`{"apiVersion":"helm.toolkit.fluxcd.io/v2","kind":"HelmRelease","metadata":{"name":"platform-components","namespace":"platform-components"},"spec":{"values":{"platform":{"components":{"apps":{"sourcegateway":{"enabled":%t},"agent-router":{"enabled":%t}},"crds":{"sourcegateway-crds":{"enabled":%t},"agent-router-crds":{"enabled":%t}}}}}}}`, enabled, enabled, enabled, enabled))},
			{Revision: 2, Key: []byte("/registry/secrets.podplane.dev/secretproviderbindings/platform-sourcegateway/sourcegateway"), Value: []byte(`{"spec":{"items":[{"key":"encryption-key","path":"encryption-key"}]}}`)},
		}
		var seed bytes.Buffer
		if err := datafile.WriteSnapshot(&seed, records, "generic"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(seedPath, seed.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		// Default Agent Router sync targets belong to the components chart, even
		// when the seed enables Agent Router and the cluster has a trust domain.
		baseline, err := netsyseed.Prepare(netsyseed.SnapshotOptions{ClusterConfigPath: configPath, SeedPath: seedPath})
		if err != nil {
			t.Fatal(err)
		}
		var baselineValues map[string]any
		if err := json.Unmarshal(baseline.ComponentValues, &baselineValues); err != nil {
			t.Fatal(err)
		}
		operatorConfig := componentValue(t, baselineValues, "platform", "components", "values", "podplane-operator", "podplane", "operator", "config").(map[string]any)
		if _, exists := operatorConfig["workloadCertificates"]; exists {
			t.Fatal("netsyseed injected chart-owned workload certificate settings")
		}
		prepared, err := netsyseed.Prepare(netsyseed.SnapshotOptions{
			ClusterConfigPath: configPath, SeedPath: seedPath, ValuesFile: valuesPath,
			ValuesContent: []byte(`{"platform":{"components":{"values":{
				"sourcegateway":{"image":"ghcr.io/example/gateway:v1"},
				"podplane-operator":{"podplane":{"operator":{"config":{
					"secrets":{"providers":{"target-provider":{"keyPrefix":"shared","region":"us-east-2"}}},
					"workloadCertificates":{"trustBundleSync":{"createIfMissing":true,"resources":[
						{"kind":"ConfigMap","namespace":"custom-router","name":"replaced-ca","key":"ca.crt","createIfMissing":true}
					]}}
				}}}},
				"agent-router":{"gateway-helm":{"config":{"envoyGateway":{"extensionManager":{"service":{
					"fqdn":{"hostname":"extension.example.com","port":1063},
					"tls":{"certificateRef":{"kind":"Secret","name":"original-ca","namespace":"custom-router"}}
				}}}}}}
			}}}}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		config, err := components.ParseValues(prepared.ComponentValues)
		if err != nil {
			t.Fatal(err)
		}
		required, err := config.RequiredSecrets(nil)
		if err != nil {
			t.Fatal(err)
		}
		if !enabled {
			if len(required) != 0 {
				t.Fatal("bootstrap enabled a component disabled in the seed")
			}
		} else if len(required) != 1 || required[0].ProviderName != "target-provider" || required[0].Provider.Region != "ap-southeast-2" || required[0].KeyPath() != "shared/platform-sourcegateway/sourcegateway/encryption-key" {
			t.Fatalf("bootstrap ignored effective values: %+v", required)
		}
		var output bytes.Buffer
		if err := prepared.WriteSnapshot(&output); err != nil {
			t.Fatal(err)
		}
		got, err := datafile.ReadSnapshot(bytes.NewReader(output.Bytes()))
		if err != nil || len(got) != len(records) {
			t.Fatalf("snapshot records: %v", err)
		}
		for _, record := range got {
			if strings.Contains(string(record.Value), `"providerName"`) {
				t.Fatal("snapshot pinned a Source Gateway provider")
			}
			if string(record.Key) != platformComponentsHelmReleaseKey {
				continue
			}
			var release map[string]any
			if err := json.Unmarshal(record.Value, &release); err != nil {
				t.Fatal(err)
			}
			values := release["spec"].(map[string]any)["values"].(map[string]any)
			var preparedValues map[string]any
			if err := json.Unmarshal(prepared.ComponentValues, &preparedValues); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(preparedValues, values) {
				t.Fatal("prepared component values differ from the published configuration")
			}
			components := values["platform"].(map[string]any)["components"].(map[string]any)
			operatorConfig := componentValue(t, components, "values", "podplane-operator", "podplane", "operator", "config").(map[string]any)
			if got := componentValue(t, operatorConfig, "cluster", "spiffe", "trustDomain"); got != cfg.Cluster.SPIFFE.TrustDomain {
				t.Fatalf("trustDomain = %v, want %s", got, cfg.Cluster.SPIFFE.TrustDomain)
			}
			wantSync := map[string]any{"createIfMissing": true, "resources": []any{
				map[string]any{"kind": "Secret", "namespace": "custom-router", "name": "extension-ca", "key": "tls.crt", "createIfMissing": false},
				map[string]any{"kind": "ConfigMap", "namespace": "custom-router", "name": "shared-ca", "key": "bundle.pem"},
			}}
			if got := componentValue(t, operatorConfig, "workloadCertificates", "trustBundleSync"); !reflect.DeepEqual(got, wantSync) {
				t.Fatalf("trustBundleSync = %#v, want %#v", got, wantSync)
			}
			wantService := map[string]any{
				"fqdn": map[string]any{"hostname": "override.example.com", "port": float64(1063)},
				"tls":  map[string]any{"certificateRef": map[string]any{"kind": "Secret", "name": "extension-ca", "namespace": "custom-router"}},
			}
			if got := componentValue(t, components, "values", "agent-router", "gateway-helm", "config", "envoyGateway", "extensionManager", "service"); !reflect.DeepEqual(got, wantService) {
				t.Fatalf("extension service = %#v, want %#v", got, wantService)
			}
			image := components["values"].(map[string]any)["sourcegateway"].(map[string]any)["image"]
			if image != "registry.example.com/mirror/ghcr.io/example/gateway:v1" {
				t.Fatalf("component image = %v, want the configured registry mirror", image)
			}
			for _, name := range []string{"sourcegateway", "agent-router"} {
				if components["apps"].(map[string]any)[name].(map[string]any)["enabled"] != enabled ||
					components["crds"].(map[string]any)[name+"-crds"].(map[string]any)["enabled"] != enabled {
					t.Fatalf("snapshot changed %s or its CRDs from seed enabled=%t", name, enabled)
				}
			}
		}
	}
}

// componentValue looks up a nested Helm value, reporting malformed paths as test failures.
func componentValue(t *testing.T, values map[string]any, path ...string) any {
	t.Helper()
	var value any = values
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("value path %v: %s parent is %T, want object", path, key, value)
		}
		value, ok = object[key]
		if !ok {
			t.Fatalf("value path %v: missing %s", path, key)
		}
	}
	return value
}
