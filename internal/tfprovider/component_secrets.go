// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/podplane/podplane/internal/secrets"
)

// ensureComponentSecret provisions a component prerequisite outside Terraform state.
// Vault/OpenBao cloud backends are provisioned externally, like their workload
// CA key; local fake Vault is handled by local.
func ensureComponentSecret(ctx context.Context, opts SeedOptions, required secrets.ProvisionSpec, open func(context.Context, cloudSecretOptions) (cloudSecretStore, error)) error {
	if required.Provider.Kind == "vault" || required.Provider.Kind == "openbao" {
		return nil
	}
	secret, err := componentSecretOptions(required, opts)
	if err != nil {
		return err
	}
	store, err := open(ctx, secret)
	if err != nil {
		return errors.New("cannot configure component secret backend")
	}
	defer func() { _ = store.Close() }()
	candidate, err := required.Generate()
	defer clear(candidate)
	if err != nil {
		return fmt.Errorf("generate component secret %q: %w", required.KeyPath(), err)
	}
	_, winner, err := store.CreateOrAdopt(ctx, candidate)
	defer clear(winner)
	if err != nil {
		return errors.New("cannot create or adopt component secret; check backend permissions and restore any archived value")
	}
	return required.Validate(winner)
}

// componentSecretOptions derives backend key names and discovery labels for prerequisites.
func componentSecretOptions(required secrets.ProvisionSpec, opts SeedOptions) (cloudSecretOptions, error) {
	provider := required.Provider
	secret := cloudSecretOptions{Region: provider.Region, Profile: opts.Profile}
	if secret.Region == "" {
		secret.Region = opts.Region
	}
	switch provider.Kind {
	case "aws":
		secret.Name = "/" + required.KeyPath()
		secret.Provider = cloudSecretProviderAWSSecretsManager
		if provider.ObjectType == "ssmparameter" {
			secret.Provider = cloudSecretProviderAWSSSM
		}
		secret.AWSTags = map[string]string{
			"podplane.dev/cluster-prefix": provider.KeyPrefix,
			"podplane.dev/namespace":      required.Namespace,
			"podplane.dev/binding":        required.Binding,
		}
	case "gcp":
		secret.Provider = cloudSecretProviderGCPSecretManager
		secret.Project = provider.ProjectID
		parent := "projects/" + provider.ProjectID
		if provider.Location != "" {
			parent += "/locations/" + provider.Location
		}
		secret.Name = parent + "/secrets/" + strings.ReplaceAll(required.KeyPath(), "/", "_")
		secret.GCPLabels = map[string]string{
			"podplane-cluster-prefix": provider.KeyPrefix,
			"podplane-namespace":      required.Namespace,
			"podplane-binding":        required.Binding,
		}
	default:
		return secret, fmt.Errorf("unsupported component secrets provider kind %q", provider.Kind)
	}
	return secret, nil
}
