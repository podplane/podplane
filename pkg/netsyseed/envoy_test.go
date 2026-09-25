// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
)

// TestInterpolateEnvoyIngressReplacesCapturedDomains verifies target domains
// replace every captured domain reference and receive synthesized SDS Secrets.
func TestInterpolateEnvoyIngressReplacesCapturedDomains(t *testing.T) {
	records := []*datafile.Record{
		{Revision: 1, Key: []byte(envoyGatewayKey), Value: []byte(`{"apiVersion":"gateway.networking.k8s.io/v1","kind":"Gateway","spec":{"listeners":[{"name":"http","protocol":"HTTP","port":80},{"name":"https-old","protocol":"HTTPS","hostname":"old.example","tls":{"certificateRefs":[{"name":"bundle-old"}]}}]}}`)},
		{Revision: 2, Key: []byte(envoyProxyKey), Value: []byte(`{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"EnvoyProxy","spec":{"provider":{"kubernetes":{"envoyDaemonSet":{"patch":{"value":{"spec":{"template":{"spec":{"containers":[{"name":"envoy"},{"name":"podplane-sds","args":["sds","--socket=/var/run/podplane-sds/sds.sock","--certificate=bundle-old=old.example"]}]}}}}}}}}}}`)},
		{Revision: 3, Key: []byte(envoySPCKey), Value: []byte(`{"apiVersion":"secrets-store.csi.x-k8s.io/v1","kind":"SecretProviderClass","spec":{"provider":"openbao","parameters":{"objects":"stale"}}}`)},
	}
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "target",
		Domains: []clusterconfig.Domain{
			{Zone: "alpha.example"},
			{Zone: "second.example.net"},
		},
		Secrets: clusterconfig.Secrets{
			DefaultProvider: "bao",
			Providers: map[string]clusterconfig.SecretsProvider{
				"bao": {Kind: "openbao", Address: "https://bao.example", MountPath: "kv", KeyPrefix: "target", CACert: "ca.pem"},
			},
		},
	}}

	got, err := interpolateEnvoyIngress(records, cfg)
	if err != nil {
		t.Fatalf("interpolateEnvoyIngress error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len(records) = %d, want 5", len(got))
	}

	gateway := decodeTestObject(t, got[0])
	listeners := gateway["spec"].(map[string]any)["listeners"].([]any)
	if len(listeners) != 5 {
		t.Fatalf("Gateway listeners = %d, want HTTP plus four HTTPS listeners", len(listeners))
	}
	wantGateway := []string{
		"alpha.example/bundle-a78c69f84ef791b5c46df59d",
		"*.alpha.example/bundle-a78c69f84ef791b5c46df59d",
		"second.example.net/bundle-42120140cb0aa293717fc6c3",
		"*.second.example.net/bundle-42120140cb0aa293717fc6c3",
	}
	for i, want := range wantGateway {
		listener := listeners[i+1].(map[string]any)
		ref := listener["tls"].(map[string]any)["certificateRefs"].([]any)[0].(map[string]any)
		if got := listener["hostname"].(string) + "/" + ref["name"].(string); got != want {
			t.Errorf("listener %d hostname/ref = %q, want %q", i, got, want)
		}
	}

	proxy := decodeTestObject(t, got[1])
	containers, err := nestedSlice(proxy, "test", "spec", "provider", "kubernetes", "envoyDaemonSet", "patch", "value", "spec", "template", "spec", "containers")
	if err != nil {
		t.Fatal(err)
	}
	args := containers[1].(map[string]any)["args"].([]any)
	wantArgs := []string{
		"--certificate=bundle-a78c69f84ef791b5c46df59d=alpha.example",
		"--certificate=bundle-42120140cb0aa293717fc6c3=second.example.net",
	}
	for i, want := range wantArgs {
		if got := args[len(args)-2+i]; got != want {
			t.Errorf("SDS arg %d = %v, want %q", i, got, want)
		}
	}

	spc := decodeTestObject(t, got[2])
	parameters := spc["spec"].(map[string]any)["parameters"].(map[string]any)
	objects := parameters["objects"].(string)
	for _, want := range []string{
		`objectName: "bundle-a78c69f84ef791b5c46df59d"`,
		`secretPath: "kv/data/target/platform-cluster/ingress-certificates/bundle-42120140cb0aa293717fc6c3"`,
	} {
		if !strings.Contains(objects, want) {
			t.Errorf("SecretProviderClass objects missing %q:\n%s", want, objects)
		}
	}
	if got, want := parameters["baoCACertPath"], "/var/run/podplane/secrets-providers/bao/ca.crt"; got != want {
		t.Errorf("baoCACertPath = %v, want %v", got, want)
	}

	for i, want := range []string{"bundle-a78c69f84ef791b5c46df59d", "bundle-42120140cb0aa293717fc6c3"} {
		record := got[3+i]
		if record.Revision != int64(4+i) || record.CreateRevision != int64(4+i) || !record.Created || record.Version != 1 {
			t.Errorf("Secret record metadata = %#v", record)
		}
		secret := decodeTestObject(t, record)
		if got := secret["metadata"].(map[string]any)["name"]; got != want {
			t.Errorf("Secret name = %v, want %s", got, want)
		}
		encoded := secret["data"].(map[string]any)["secretName"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded) != want {
			t.Errorf("Secret data.secretName = %q, want %q", decoded, want)
		}
	}
}

// TestEnvoyBundleNameMatchesHelmHelper verifies the hash contract shared with
// the Envoy Gateway Helm chart.
func TestEnvoyBundleNameMatchesHelmHelper(t *testing.T) {
	if got, want := envoyBundleName("default.localhost"), "bundle-9464ba90046c9f91c931fcb4"; got != want {
		t.Fatalf("envoyBundleName() = %q, want %q", got, want)
	}
}

// TestInterpolateEnvoyIngressLeavesMinimalSeedUnchanged verifies seeds without
// Envoy ingress resources do not receive synthesized resources.
func TestInterpolateEnvoyIngressLeavesMinimalSeedUnchanged(t *testing.T) {
	records := []*datafile.Record{{Revision: 1, Key: []byte(platformComponentsHelmReleaseKey), Value: []byte(`{}`)}}
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{Domains: []clusterconfig.Domain{{Zone: "example.com"}}}}
	got, err := interpolateEnvoyIngress(records, cfg)
	if err != nil {
		t.Fatalf("interpolateEnvoyIngress error = %v", err)
	}
	if len(got) != 1 || got[0] != records[0] {
		t.Fatalf("minimal records changed: %#v", got)
	}
}

// decodeTestObject decodes a Kubernetes record for test assertions.
func decodeTestObject(t *testing.T, record *datafile.Record) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(record.Value, &obj); err != nil {
		t.Fatalf("decode %s: %v", record.Key, err)
	}
	return obj
}
