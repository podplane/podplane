// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/secrets"
)

// TestRequiredSecrets verifies effective provider selection for enabled and planned apps.
func TestRequiredSecrets(t *testing.T) {
	for _, tc := range []struct {
		name               string
		provider, fallback any
		enabled            bool
		apps               []string
		want               []secrets.ProvisionSpec
		wantErr            bool
		wantDecodeErr      bool
	}{
		{name: "default", enabled: true, fallback: "default", want: []secrets.ProvisionSpec{{ProviderName: "default", Provider: clusterconfig.SecretsProvider{Kind: "aws", ObjectType: "ssmparameter", Region: "us-east-2", KeyPrefix: "cluster"}, Namespace: "gateway", Binding: "sourcegateway", Key: "encryption-key"}}},
		{name: "alternate cluster default", enabled: true, fallback: "alternate", want: []secrets.ProvisionSpec{{ProviderName: "alternate", Provider: clusterconfig.SecretsProvider{Kind: "gcp", ProjectID: "project", Location: "us-west1", KeyPrefix: "shared"}, Namespace: "gateway", Binding: "sourcegateway", Key: "encryption-key"}}},
		{name: "planned disabled app", apps: []string{"sourcegateway"}, fallback: "default", want: []secrets.ProvisionSpec{{ProviderName: "default", Provider: clusterconfig.SecretsProvider{Kind: "aws", ObjectType: "ssmparameter", Region: "us-east-2", KeyPrefix: "cluster"}, Namespace: "gateway", Binding: "sourcegateway", Key: "encryption-key"}}},
		{name: "unrelated install", enabled: true, apps: []string{"agent-router"}},
		{name: "disabled"},
		{name: "missing default", enabled: true, wantErr: true},
		{name: "unknown default", enabled: true, fallback: "unknown", wantErr: true},
		{name: "obsolete override ignored", enabled: true, provider: "alternate", fallback: "default", want: []secrets.ProvisionSpec{{ProviderName: "default", Provider: clusterconfig.SecretsProvider{Kind: "aws", ObjectType: "ssmparameter", Region: "us-east-2", KeyPrefix: "cluster"}, Namespace: "gateway", Binding: "sourcegateway", Key: "encryption-key"}}},
		{name: "invalid default type", enabled: true, fallback: []string{"default"}, wantDecodeErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]any{"platform": map[string]any{"components": map[string]any{
				"apps": map[string]any{"sourcegateway": map[string]any{"enabled": tc.enabled, "namespace": "gateway"}},
				"values": map[string]any{
					"unrelated-chart": map[string]any{"arbitrary": []any{true, 42, "value"}},
					"sourcegateway":   map[string]any{"platform": map[string]any{"sourceGateway": map[string]any{"providerName": tc.provider}}},
					"podplane-operator": map[string]any{"podplane": map[string]any{"operator": map[string]any{"config": map[string]any{
						"cluster": map[string]any{"id": "cluster"},
						"secrets": map[string]any{"defaultProvider": tc.fallback, "providers": map[string]any{
							"default":   map[string]any{"kind": "aws", "objectType": "ssmparameter", "region": "us-east-2"},
							"alternate": map[string]any{"kind": "gcp", "projectID": "project", "location": "us-west1", "keyPrefix": "shared"},
						}},
					}}}},
				},
			}}}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := ParseValues(encoded)
			if tc.wantDecodeErr {
				if err == nil {
					t.Fatal("expected invalid configuration to fail decoding")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := cfg.RequiredSecrets(tc.apps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("bootstrap error = %v, want error=%t", err, tc.wantErr)
			}
			for i := range got {
				if got[i].Generate == nil || got[i].Validate == nil {
					t.Fatal("missing component secret callbacks")
				}
				got[i].Generate, got[i].Validate = nil, nil
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("bootstrap = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSourceGatewayEncryptionKey checks generation and validation of the required
// 32-byte base64 key, without disclosing invalid values in errors.
func TestSourceGatewayEncryptionKey(t *testing.T) {
	cfg := &Config{
		ClusterID: "cluster",
		Secrets: clusterconfig.Secrets{
			DefaultProvider: "default",
			Providers: map[string]clusterconfig.SecretsProvider{
				"default": {Kind: "aws"},
			},
		},
	}
	required, err := cfg.RequiredSecrets([]string{"sourcegateway"})
	if err != nil || len(required) != 1 {
		t.Fatalf("RequiredSecrets = %v, %v", required, err)
	}
	r := required[0]
	a, err := r.Generate()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(a)
	b, err := r.Generate()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(b)
	if bytes.Equal(a, b) {
		t.Fatal("generation did not produce independent keys")
	}
	for _, value := range [][]byte{a, b} {
		decoded, err := base64.StdEncoding.Strict().DecodeString(string(value))
		if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != string(value) {
			t.Fatal("generation did not produce a canonical base64-encoded 32-byte key")
		}
		clear(decoded)
		if err := r.Validate(value); err != nil {
			t.Fatal(err)
		}
	}
	valid := strings.Repeat("A", 43) + "="
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"canonical", valid, true},
		{"surrounding whitespace", " \t" + valid + "\n", true},
		{"nonzero padding bits", strings.Repeat("A", 42) + "B=", false},
		{"missing padding", strings.TrimSuffix(valid, "="), false},
		{"embedded newline", valid[:5] + "\n" + valid[5:], false},
		{"embedded carriage return", valid[:5] + "\r" + valid[5:], false},
		{"URL alphabet", strings.Repeat("_", 42) + "8=", false},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 31)), false},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 33)), false},
		{"empty", "", false},
		{"plaintext", "private-value", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := r.Validate([]byte(tc.value))
			if (err == nil) != tc.valid {
				t.Fatalf("Validate = %v, want valid=%t", err, tc.valid)
			}
			if err != nil && tc.value != "" && strings.Contains(err.Error(), tc.value) {
				t.Fatal("validation error disclosed value")
			}
		})
	}
}
