// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package netsyseed

import (
	"encoding/json"
	"testing"

	"github.com/netsy-dev/netsy/pkg/datafile"
	"github.com/podplane/podplane/internal/clusterconfig"
)

// capturedOperatorSPC is the workload CA SecretProviderClass captured from a
// local cluster named default.
const capturedOperatorSPC = `{"kind":"SecretProviderClass","metadata":{"name":"platform-podplane-operator"},"spec":{"provider":"openbao","parameters":{"baoAddress":"https://10.0.2.15:19443/vault/default","baoAuthMountPath":"kubernetes","baoCACertPath":"/var/run/podplane/secrets-providers/local-fakevault/ca.crt","roleName":"podplane-operator","objects":"- objectName: workload-ca-key\n  secretPath: \"secret/data/default/workload-ca-key\"\n  secretKey: value"}}}`

// TestInterpolateSecretsProvidersReplacesCapturedSettings checks pods that read
// provider settings once at startup see this cluster's, not the capture's.
func TestInterpolateSecretsProvidersReplacesCapturedSettings(t *testing.T) {
	const (
		openbaoCAKey = "/registry/configmaps/platform-secrets-store-csi-provider-openbao/podplane-secrets-provider-ca-"
		vaultCAKey   = "/registry/configmaps/platform-secrets-store-csi-provider-vault/podplane-secrets-provider-ca-"
		agentKey     = "/registry/configmaps/platform-secrets-store-csi-provider-openbao/platform-secrets-store-csi-provider-openbao-csi-provider-agent-config"
	)
	captured := `{"kind":"ConfigMap","data":{"ca.crt":"captured-ca"}}`
	records := []*datafile.Record{
		{Key: []byte(openbaoCAKey + "local-fakevault"), Value: []byte(captured)},
		{Key: []byte(openbaoCAKey + "removed"), Value: []byte(captured)},
		{Key: []byte(openbaoCAKey + "now-vault"), Value: []byte(captured)},
		{Key: []byte(vaultCAKey + "now-vault"), Value: []byte(captured)},
		{Key: []byte(agentKey), Value: []byte(captured)},
		{Key: []byte(operatorConfigKey), Value: []byte(`{"kind":"ConfigMap","data":{"config.json":"{\"cluster\":{\"id\":\"captured\"},\"secrets\":{\"key_rotation\":\"6h\",\"default_provider\":\"removed\",\"providers\":{\"removed\":{\"kind\":\"openbao\",\"ca_cert\":\"captured-ca\"}}}}"}}`)},
		{Key: []byte(operatorSPCKey), Value: []byte(capturedOperatorSPC)},
	}
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "dev",
		Secrets: clusterconfig.Secrets{DefaultProvider: "local-fakevault", Providers: map[string]clusterconfig.SecretsProvider{
			"local-fakevault": {Kind: "openbao", Address: "https://10.0.2.15:19443/vault/dev", MountPath: "secret", CACert: "cluster-ca"},
			"now-vault":       {Kind: "vault", Address: "https://vault.example.com", CACert: "vault-ca", OperatorRole: "operator"},
		}},
	}}

	got, err := interpolateSecretsProviders(records, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cas := map[string]any{}
	for _, record := range got {
		obj := decodeTestObject(t, record)
		switch key := string(record.Key); key {
		case operatorConfigKey:
			var config map[string]any
			if err := json.Unmarshal([]byte(obj["data"].(map[string]any)["config.json"].(string)), &config); err != nil {
				t.Fatal(err)
			}
			assertJSON(t, "operator secrets", config["secrets"], map[string]any{
				"key_rotation":     "6h",
				"default_provider": "local-fakevault",
				"providers": map[string]any{
					"local-fakevault": map[string]any{"kind": "openbao", "address": "https://10.0.2.15:19443/vault/dev", "mount_path": "secret", "ca_cert": "cluster-ca"},
					"now-vault":       map[string]any{"kind": "vault", "address": "https://vault.example.com", "ca_cert": "vault-ca", "operator_role": "operator"},
				},
			})
			assertJSON(t, "operator cluster", config["cluster"], map[string]any{"id": "captured"})
		case operatorSPCKey:
			assertJSON(t, "operator SecretProviderClass parameters", obj["spec"].(map[string]any)["parameters"], map[string]any{
				"baoAddress":       "https://10.0.2.15:19443/vault/dev",
				"baoAuthMountPath": "kubernetes",
				"baoCACertPath":    "/var/run/podplane/secrets-providers/local-fakevault/ca.crt",
				"roleName":         "podplane-operator",
				"objects":          "- objectName: workload-ca-key\n  secretPath: \"secret/data/dev/workload-ca-key\"\n  secretKey: value",
			})
		default:
			cas[key] = obj["data"].(map[string]any)["ca.crt"]
		}
	}
	assertJSON(t, "CA ConfigMaps", cas, map[string]any{
		openbaoCAKey + "local-fakevault": "cluster-ca",
		vaultCAKey + "now-vault":         "vault-ca",
		agentKey:                         "captured-ca",
	})
}

// TestInterpolateSecretsProvidersCreatesMissingRecords checks seeds that omit
// the CA ConfigMaps and operator SecretProviderClass still get them, owned by
// their Helm releases, but only for charts whose namespace the seed contains.
func TestInterpolateSecretsProvidersCreatesMissingRecords(t *testing.T) {
	const (
		caKey      = "/registry/configmaps/platform-secrets-store-csi-provider-openbao/podplane-secrets-provider-ca-local-fakevault"
		vaultCAKey = "/registry/configmaps/platform-secrets-store-csi-provider-vault/podplane-secrets-provider-ca-team-vault"
	)
	records := []*datafile.Record{
		{Key: []byte("/registry/namespaces/platform-podplane-operator"), Value: []byte(`{"kind":"Namespace"}`)},
		{Key: []byte("/registry/namespaces/platform-secrets-store-csi-provider-openbao"), Value: []byte(`{"kind":"Namespace"}`)},
	}
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID: "dev",
		Secrets: clusterconfig.Secrets{DefaultProvider: "local-fakevault", Providers: map[string]clusterconfig.SecretsProvider{
			"local-fakevault": {Kind: "openbao", Address: "https://10.0.2.15:19443/vault/dev", CACert: "cluster-ca"},
			"team-vault":      {Kind: "vault", Address: "https://vault.example.com", CACert: "vault-ca"},
		}},
	}}

	got, err := interpolateSecretsProviders(records, cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := interpolateSecretsProviders(got, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || len(again) != 4 {
		t.Fatalf("records = %d then %d, want 4 both times", len(got), len(again))
	}
	created := map[string]map[string]any{}
	for _, record := range got {
		created[string(record.Key)] = decodeTestObject(t, record)
	}
	if _, ok := created[vaultCAKey]; ok {
		t.Fatal("created a CA ConfigMap in a namespace the seed lacks")
	}
	for key, release := range map[string]string{
		caKey:          "platform-secrets-store-csi-provider-openbao",
		operatorSPCKey: "platform-podplane-operator",
	} {
		obj, ok := created[key]
		if !ok {
			t.Fatalf("missing %s", key)
		}
		metadata := obj["metadata"].(map[string]any)
		assertJSON(t, key+" annotations", metadata["annotations"], map[string]any{
			"meta.helm.sh/release-name":      release,
			"meta.helm.sh/release-namespace": release,
		})
		if metadata["namespace"] != release || metadata["uid"] == "" {
			t.Fatalf("%s metadata = %v", key, metadata)
		}
	}
	assertJSON(t, "CA ConfigMap data", created[caKey]["data"], map[string]any{"ca.crt": "cluster-ca"})
	spec := created[operatorSPCKey]["spec"].(map[string]any)
	if spec["provider"] != "openbao" || spec["parameters"].(map[string]any)["baoAddress"] != "https://10.0.2.15:19443/vault/dev" {
		t.Fatalf("SecretProviderClass spec = %v", spec)
	}
}

// TestSetWorkloadCASecretProviderClass checks the captured OpenBao spec is
// replaced with the chart's spec for each kind of default provider.
func TestSetWorkloadCASecretProviderClass(t *testing.T) {
	tests := []struct {
		name     string
		provider clusterconfig.SecretsProvider
		want     map[string]any
	}{
		{
			name:     "aws",
			provider: clusterconfig.SecretsProvider{Kind: "aws", ObjectType: "secretsmanager", KeyPrefix: "/team/prod/"},
			want: map[string]any{"provider": "aws", "parameters": map[string]any{
				"objects": "- objectName: \"/team/prod/workload-ca-key\"\n  objectType: \"secretsmanager\"\n  objectAlias: workload-ca-key",
			}},
		},
		{
			name:     "gcp",
			provider: clusterconfig.SecretsProvider{Kind: "gcp", ProjectID: "acme"},
			want: map[string]any{"provider": "gcp", "parameters": map[string]any{
				"auth":    "provider-adc",
				"secrets": "- resourceName: \"projects/acme/secrets/prod_workload-ca-key/versions/latest\"\n  path: workload-ca-key",
			}},
		},
		{
			name:     "vault",
			provider: clusterconfig.SecretsProvider{Kind: "vault", Address: "https://vault.example.com", MountPath: "/kv/", AuthPath: "auth/jwt/", OperatorRole: "operator"},
			want: map[string]any{"provider": "vault", "parameters": map[string]any{
				"vaultAddress":       "https://vault.example.com",
				"vaultAuthMountPath": "jwt",
				"roleName":           "operator",
				"objects":            "- objectName: workload-ca-key\n  secretPath: \"kv/data/prod/workload-ca-key\"\n  secretKey: value",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spc map[string]any
			if err := json.Unmarshal([]byte(capturedOperatorSPC), &spc); err != nil {
				t.Fatal(err)
			}
			secrets := clusterconfig.Secrets{DefaultProvider: "backend", Providers: map[string]clusterconfig.SecretsProvider{"backend": tt.provider}}
			if err := setWorkloadCASecretProviderClass(spc, "prod", secrets); err != nil {
				t.Fatal(err)
			}
			assertJSON(t, "spec", spc["spec"], tt.want)
		})
	}
}

// assertJSON fails the test when got and want encode to different JSON.
func assertJSON(t *testing.T, name string, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s = %s, want %s", name, gotJSON, wantJSON)
	}
}
