// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"encoding/json"
	"testing"
)

func TestComponentsUnmarshalObject(t *testing.T) {
	var cfg ClusterConfig
	if err := json.Unmarshal([]byte(`{"cluster":{"components":{"source":{"url":"https://github.com/example/components.git","ref":{"branch":"feature"}}}}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got, want := cfg.Cluster.Components.Source.URL, "https://github.com/example/components.git"; got != want {
		t.Fatalf("components.source.url = %q, want %q", got, want)
	}
	if got, want := cfg.Cluster.Components.Source.Ref.Branch, "feature"; got != want {
		t.Fatalf("components.source.ref.branch = %q, want %q", got, want)
	}
}

func TestValidateComponentsRejectsMultipleSourceRefs(t *testing.T) {
	err := ValidateComponents(Components{Source: &ComponentsSource{Ref: ComponentsSourceRef{Branch: "main", Tag: "v1.0.0"}}})
	if err == nil {
		t.Fatal("ValidateComponents returned nil, want error")
	}
}

func TestValidateComponentsRejectsEmptySourceSecretRefName(t *testing.T) {
	err := ValidateComponents(Components{Source: &ComponentsSource{URL: "https://github.com/example/components.git", SecretRef: &ComponentsSourceSecretRef{}}})
	if err == nil {
		t.Fatal("ValidateComponents returned nil, want error")
	}
}

func TestValidateClusterID(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		expectErr bool
	}{
		{name: "valid", id: "test-cluster-123"},
		{name: "default", id: "default"},
		{name: "one letter", id: "a"},
		{name: "one number", id: "1"},
		{name: "starts with number", id: "1test"},
		{name: "empty", id: "", expectErr: true},
		{name: "invalid characters", id: "test@cluster", expectErr: true},
		{name: "too long", id: "this-is-a-very-long-cluster-id-that-exceeds-the-maximum", expectErr: true},
		{name: "spaces", id: "test cluster", expectErr: true},
		{name: "uppercase", id: "Test-Cluster", expectErr: true},
		{name: "starts with hyphen", id: "-test", expectErr: true},
		{name: "ends with hyphen", id: "test-", expectErr: true},
		{name: "consecutive hyphens", id: "test--cluster", expectErr: true},
		{name: "underscore", id: "test_cluster", expectErr: true},
		{name: "reserved local", id: "local", expectErr: true},
		{name: "reserved k8s", id: "k8s", expectErr: true},
		{name: "reserved oidc", id: "oidc", expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClusterID(tt.id)
			if (err != nil) != tt.expectErr {
				t.Fatalf("ValidateClusterID(%q) error = %v, expectErr %v", tt.id, err, tt.expectErr)
			}
		})
	}
}
