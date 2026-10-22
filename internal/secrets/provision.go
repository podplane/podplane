// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"fmt"
	"path"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// ProvisionSpec describes an internally defined secret and how to provision it.
type ProvisionSpec struct {
	ProviderName, Namespace, Binding, Key string
	Provider                              clusterconfig.SecretsProvider
	// Generate returns a new value whose buffer the caller owns and clears.
	Generate func() ([]byte, error)
	// Validate checks an existing value without disclosing it in errors.
	Validate func([]byte) error
}

// KeyPath returns the provider's backend path for the required secret.
func (r ProvisionSpec) KeyPath() string {
	return path.Join(r.Provider.KeyPrefix, r.Namespace, r.Binding, r.Key)
}

// Ensure preserves active secrets and creates missing secrets without overwriting
// or restoring existing entries. Metadata cannot validate existing plaintext.
func (c Client) Ensure(clusterID string, requirement ProvisionSpec) error {
	name := KeyspaceName(requirement.ProviderName, requirement.Binding)
	metadata, err := c.Get(requirement.Namespace, name)
	if err != nil {
		return fmt.Errorf("read required secret metadata: %w", err)
	}
	for _, entry := range metadata.Status.Entries {
		if entry.Key == requirement.Key {
			if entry.Status == "active" {
				return nil
			}
			return fmt.Errorf("required secret %q has status %q; explicitly restore or resolve the existing entry before retrying", entry.Key, entry.Status)
		}
	}
	value, err := requirement.Generate()
	defer clear(value)
	if err != nil {
		return fmt.Errorf("generate required secret: %w", err)
	}
	opts := WriteOptions{ClusterID: clusterID, Namespace: requirement.Namespace, KeyspaceName: name, Key: requirement.Key, Operation: "create", Value: value}
	request, err := c.EncryptedRequest(opts)
	if err != nil {
		return fmt.Errorf("encrypt required secret: %w", err)
	}
	if _, err = c.PutEncrypted(request, opts); err == nil {
		return nil
	}
	metadata, readErr := c.Get(requirement.Namespace, name)
	if readErr != nil {
		return fmt.Errorf("create required secret: %w; reread metadata: %w", err, readErr)
	}
	for _, entry := range metadata.Status.Entries {
		if entry.Key == requirement.Key && entry.Status == "active" {
			return nil
		}
	}
	return fmt.Errorf("create required secret: %w", err)
}
