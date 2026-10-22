// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package components

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"

	"github.com/podplane/podplane/internal/secrets"
)

// RequiredSecrets describes external prerequisites without generating values or
// accessing a backend. A nil apps list selects the currently enabled apps;
// install supplies its planned apps so prerequisites exist before enablement.
func (c *Config) RequiredSecrets(apps []string) ([]secrets.ProvisionSpec, error) {
	if apps == nil {
		for name, app := range c.Apps {
			if app.Enabled {
				apps = append(apps, name)
			}
		}
		slices.Sort(apps)
	}
	var requirements []secrets.ProvisionSpec
	for _, app := range apps {
		for _, requirement := range componentSecrets[app] {
			if namespace := c.Apps[app].Namespace; namespace != "" {
				requirement.Namespace = namespace
			}
			requirement.ProviderName = c.Secrets.DefaultProvider
			provider, ok := c.Secrets.Providers[requirement.ProviderName]
			if requirement.ProviderName == "" || !ok {
				return nil, fmt.Errorf("%s %s requires a configured default secrets provider", app, requirement.Key)
			}
			if provider.KeyPrefix == "" {
				provider.KeyPrefix = c.ClusterID
			}
			if provider.KeyPrefix == "" || provider.Kind == "" {
				return nil, fmt.Errorf("%s %s requires a provider kind and key prefix or cluster ID", app, requirement.Key)
			}
			requirement.Provider = provider
			requirements = append(requirements, requirement)
		}
	}
	return requirements, nil
}

// SecretApps lists the components that declare required secrets, so local
// clusters can provision them before any component is enabled.
func SecretApps() []string {
	return slices.Sorted(maps.Keys(componentSecrets))
}

const sourceGatewayKeyBytes = 32

// componentSecrets declares each component's keys and generation requirements.
var componentSecrets = map[string][]secrets.ProvisionSpec{
	"sourcegateway": {{
		Namespace: "platform-sourcegateway",
		Binding:   "sourcegateway", Key: "encryption-key",
		Generate: func() ([]byte, error) {
			raw := make([]byte, sourceGatewayKeyBytes)
			defer clear(raw)
			rand.Read(raw)
			return base64.StdEncoding.AppendEncode(nil, raw), nil
		},
		Validate: func(value []byte) error {
			value = bytes.TrimSpace(value)
			decoded := make([]byte, base64.StdEncoding.DecodedLen(len(value)))
			defer clear(decoded)
			n, err := base64.StdEncoding.Strict().Decode(decoded, value)
			if err != nil || bytes.ContainsAny(value, "\r\n") {
				return fmt.Errorf("secret must be canonical standard base64")
			}
			if n != sourceGatewayKeyBytes {
				return fmt.Errorf("secret must decode to exactly %d bytes", sourceGatewayKeyBytes)
			}
			return nil
		},
	}},
}
