// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"encoding/base64"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/fakevault"
)

// TestLocalComponentSecrets checks create-only provisioning through the real local
// Vault transport, including refusing to route remote providers locally.
func TestLocalComponentSecrets(t *testing.T) {
	path := filepath.Join(filepath.Dir(shortSocketPath(t)), localVaultSocketName)
	listener, err := localVaultListener(path)
	if err != nil {
		t.Fatal(err)
	}
	store := fakevault.NewFileStore(localVaultTestKeyring{}, t.TempDir())
	server := &http.Server{Handler: fakevault.NewTrustedHandler(store)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	m := newTestLocalManager(t.TempDir(), "dev")
	m.runtimeDir = filepath.Dir(path)
	address, err := m.localVaultServerURLForConfig("dev")
	if err != nil {
		t.Fatal(err)
	}
	var localConfig *components.Config
	for _, remote := range []bool{false, true} {
		if remote {
			address = "https://vault.example.com"
		}
		cfg := &components.Config{
			ClusterID: "dev",
			Apps:      map[string]components.Entry{"sourcegateway": {Enabled: true}},
			Secrets: clusterconfig.Secrets{
				DefaultProvider: "local",
				Providers: map[string]clusterconfig.SecretsProvider{
					"local": {Kind: "openbao", Address: address, MountPath: "/secret/", KeyPrefix: "shared"},
				},
			},
		}
		if !remote {
			localConfig = cfg
		}
		if err := m.prepareComponentSecrets(cfg); (err != nil) != remote {
			t.Fatalf("remote=%t: bootstrap error = %v", remote, err)
		}
	}
	client := newLocalVaultClient(path)
	keyPath := "secret/data/shared/platform-sourcegateway/sourcegateway/encryption-key"
	values, ok, err := client.GetSecret("dev", keyPath)
	if err != nil || !ok {
		t.Fatalf("missing key in effective keyspace: %v", err)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(values["value"])
	if err != nil || len(key) != 32 {
		t.Fatal("local bootstrap did not publish a valid Source Gateway key")
	}
	if err := m.prepareComponentSecrets(localConfig); err != nil {
		t.Fatal(err)
	}
	reused, ok, err := client.GetSecret("dev", keyPath)
	if err != nil || !ok || reused["value"] != values["value"] {
		t.Fatalf("retry changed the local key: %v", err)
	}
	if err := store.SetSecret("dev", keyPath, map[string]string{"value": "invalid-original-key"}); err != nil {
		t.Fatal(err)
	}
	if err := m.prepareComponentSecrets(localConfig); err == nil {
		t.Fatal("local bootstrap must reject an invalid existing key")
	}
	reused, ok, err = client.GetSecret("dev", keyPath)
	if err != nil || !ok || reused["value"] != "invalid-original-key" {
		t.Fatal("local bootstrap replaced an invalid existing key")
	}
}
