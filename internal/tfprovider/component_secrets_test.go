// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/secrets"
)

// TestComponentSecretBootstrap verifies creation, reuse, and rejection of invalid existing keys.
func TestComponentSecretBootstrap(t *testing.T) {
	opts := SeedOptions{}
	generation := 0
	required := secrets.ProvisionSpec{
		Provider:  clusterconfig.SecretsProvider{Kind: "aws", ObjectType: "ssmparameter", KeyPrefix: "testcluster"},
		Namespace: "app", Binding: "credentials", Key: "token",
		Generate: func() ([]byte, error) {
			generation++
			return fmt.Appendf(nil, "credential-%d", generation), nil
		},
		Validate: func(value []byte) error {
			if !strings.HasPrefix(string(value), "credential-") {
				return errors.New("invalid credential")
			}
			return nil
		},
	}
	backend := &registrationSSM{}
	store := &awsSSMStore{client: backend, name: "/testcluster/app/credentials/token"}
	open := func(_ context.Context, secret cloudSecretOptions) (cloudSecretStore, error) {
		if secret.Name != store.name || secret.Provider != cloudSecretProviderAWSSSM || secret.AWSTags["podplane.dev/binding"] != "credentials" {
			t.Fatal("incorrect backend keyspace or discovery tags")
		}
		return store, nil
	}
	var original []byte
	for range 2 {
		if err := ensureComponentSecret(context.Background(), opts, required, open); err != nil {
			t.Fatal(err)
		}
		if original != nil && !bytes.Equal(original, backend.value) {
			t.Fatal("bootstrap rotated the original key")
		}
		original = bytes.Clone(backend.value)
	}
	if string(backend.value) != "credential-1" {
		t.Fatal("bootstrap did not preserve the first generated value")
	}
	backend.value = []byte("invalid-original-key")
	if err := ensureComponentSecret(context.Background(), opts, required, open); err == nil || string(backend.value) != "invalid-original-key" {
		t.Fatal("bootstrap must reject, not replace, an invalid existing key")
	}
	backend.value = nil
	generationErr := errors.New("cannot generate credential")
	required.Generate = func() ([]byte, error) { return nil, generationErr }
	if err := ensureComponentSecret(context.Background(), opts, required, open); !errors.Is(err, generationErr) || backend.value != nil {
		t.Fatalf("generation failure must prevent creation: %v", err)
	}
}

// TestComponentSecretBackendPaths checks provider-specific names and key-prefix overrides.
func TestComponentSecretBackendPaths(t *testing.T) {
	for _, tc := range []struct {
		provider clusterconfig.SecretsProvider
		name     string
	}{
		{clusterconfig.SecretsProvider{Kind: "aws", KeyPrefix: "cluster"}, "/cluster/app/credentials/token"},
		{clusterconfig.SecretsProvider{Kind: "aws", ObjectType: "ssmparameter", KeyPrefix: "shared"}, "/shared/app/credentials/token"},
		{clusterconfig.SecretsProvider{Kind: "gcp", ProjectID: "project", KeyPrefix: "cluster"}, "projects/project/secrets/cluster_app_credentials_token"},
		{clusterconfig.SecretsProvider{Kind: "gcp", ProjectID: "project", Location: "us-west1", KeyPrefix: "shared"}, "projects/project/locations/us-west1/secrets/shared_app_credentials_token"},
	} {
		required := secrets.ProvisionSpec{Provider: tc.provider, Namespace: "app", Binding: "credentials", Key: "token"}
		secret, err := componentSecretOptions(required, SeedOptions{})
		if err != nil || secret.Name != tc.name {
			t.Fatalf("backend path = %s, %v; want %s", secret.Name, err, tc.name)
		}
	}
}
