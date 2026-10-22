// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/internal/components"
	"github.com/podplane/podplane/internal/secrets"
)

// prepareComponentSecrets provisions every component's prerequisites through
// local Vault, whether or not the component is enabled yet, so seeded clusters,
// local bootstraps and later installs all find them. It never redirects a
// remote provider's secrets into the local backend.
func (m *Local) prepareComponentSecrets(cfg *components.Config) error {
	required, err := cfg.RequiredSecrets(components.SecretApps())
	if err != nil {
		return err
	}
	for _, secret := range required {
		address, err := m.localVaultServerURLForConfig(m.clusterID)
		if err != nil {
			return err
		}
		if secret.Provider.Kind != "openbao" || secret.Provider.Address != address {
			return fmt.Errorf("local component secret %q requires the local OpenBao secrets provider", secret.KeyPath())
		}
		mount := strings.Trim(secret.Provider.MountPath, "/")
		if mount == "" {
			mount = "secret"
		}
		vault := newLocalVaultClient(filepath.Join(m.runtimeDir, localVaultSocketName))
		if err := ensureComponentSecret(vault, m.clusterID, mount+"/data/"+secret.KeyPath(), secret); err != nil {
			return err
		}
	}
	return nil
}

// ensureComponentSecret creates a prerequisite once, validating an existing value
// without ever overwriting it.
func ensureComponentSecret(store localSecretStore, clusterID, path string, secret secrets.ProvisionSpec) error {
	candidate, err := secret.Generate()
	defer clear(candidate)
	if err != nil {
		return fmt.Errorf("generate component secret %q: %w", secret.KeyPath(), err)
	}
	created, err := store.CreateSecret(clusterID, path, map[string]string{"value": string(candidate)})
	if err != nil {
		return fmt.Errorf("create component secret %q: %w", secret.KeyPath(), err)
	}
	if created {
		return nil
	}
	values, ok, err := store.GetSecret(clusterID, path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("component secret %q disappeared during creation", secret.KeyPath())
	}
	value := []byte(values["value"])
	defer clear(value)
	return secret.Validate(value)
}
