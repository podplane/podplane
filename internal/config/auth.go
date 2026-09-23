// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// AuthMetadata is the per-(sub, cluster) metadata the CLI needs to refresh
// tokens and emit kubectl ExecCredentials.
type AuthMetadata struct {
	Sub              string `mapstructure:"sub" json:"sub"`
	ClusterID        string `mapstructure:"cluster_id" json:"cluster_id"`
	ClusterName      string `mapstructure:"cluster_name" json:"cluster_name"`
	Issuer           string `mapstructure:"issuer" json:"issuer"`
	ClientID         string `mapstructure:"client_id" json:"client_id"`
	UserEmail        string `mapstructure:"user_email" json:"user_email"`
	IdentityProvider string `mapstructure:"identity_provider" json:"identity_provider"`
	IdentityFile     string `mapstructure:"identity_file" json:"identity_file"`
	OIDCCAPath       string `mapstructure:"oidc_ca_path" json:"oidc_ca_path"`
}

// AuthRef uniquely identifies auth config for local or remote clusters.
type AuthRef struct {
	Subject   string
	ClusterID string
	Local     bool
}

// AuthSecrets holds the actual tokens. Stored in the OS keyring at
// `dev.podplane.auth.<auth-key>`.
type AuthSecrets struct {
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

// keyringPrefix is the prefix used for keyring entries that store auth
// secrets. The full key is `<keyringPrefix><auth-key>`.
const keyringPrefix = "dev.podplane.auth."

// authKey returns the config and keyring key for an auth reference.
func authKey(ref AuthRef) string {
	sum := sha256.Sum256([]byte(ref.Subject + "\x00" + ref.ClusterID))
	return fmt.Sprintf("%s%x", authKeyPrefix(ref.Local), sum[:])
}

// authKeyPrefix returns the storage prefix for an auth locality.
func authKeyPrefix(local bool) string {
	if local {
		return "local:"
	}
	return "remote:"
}

// AuthGet returns the metadata and secrets for ref.
// Returns zero values (and no error) if the entry does not exist.
func (c *Config) AuthGet(ref AuthRef) (AuthMetadata, AuthSecrets, error) {
	meta, secrets, err := c.authGetKey(authKey(ref))
	if err != nil {
		return meta, secrets, fmt.Errorf("read auth for subject %q on cluster %q: %w", ref.Subject, ref.ClusterID, err)
	}
	return meta, secrets, nil
}

// authGetKey reads auth metadata and secrets by their fully-qualified auth key.
func (c *Config) authGetKey(key string) (AuthMetadata, AuthSecrets, error) {
	var meta AuthMetadata
	if raw := c.viperFile.GetStringMap("auth." + key); len(raw) > 0 {
		if err := decodeMap(raw, &meta); err != nil {
			return AuthMetadata{}, AuthSecrets{}, fmt.Errorf("decode auth metadata for %s: %w", key, err)
		}
	}
	secretsBytes, err := c.KeyringRead(keyringPrefix + key)
	if err != nil {
		return meta, AuthSecrets{}, fmt.Errorf("read keyring for %s: %w", key, err)
	}
	var secrets AuthSecrets
	if len(secretsBytes) > 0 {
		if err := json.Unmarshal(secretsBytes, &secrets); err != nil {
			return meta, AuthSecrets{}, fmt.Errorf("parse keyring entry for %s: %w", key, err)
		}
	}
	return meta, secrets, nil
}

// AuthSet writes metadata to config and secrets to the keyring.
func (c *Config) AuthSet(meta AuthMetadata, secrets AuthSecrets, local bool) error {
	if meta.Sub == "" || meta.ClusterID == "" {
		return fmt.Errorf("AuthSet: subject and cluster ID are required")
	}
	key := authKey(AuthRef{Subject: meta.Sub, ClusterID: meta.ClusterID, Local: local})

	// Write metadata to viper config.
	c.viperFile.Set("auth."+key, map[string]any{
		"sub":               meta.Sub,
		"cluster_id":        meta.ClusterID,
		"cluster_name":      meta.ClusterName,
		"issuer":            meta.Issuer,
		"client_id":         meta.ClientID,
		"user_email":        meta.UserEmail,
		"identity_provider": meta.IdentityProvider,
		"identity_file":     meta.IdentityFile,
		"oidc_ca_path":      meta.OIDCCAPath,
	})
	if err := c.SaveFile(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Write secrets to keyring.
	secretsBytes, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}
	if err := c.KeyringWrite(keyringPrefix+key, secretsBytes); err != nil {
		return fmt.Errorf("write auth secrets for subject %q on cluster %q: %w", meta.Sub, meta.ClusterID, err)
	}
	return nil
}

// AuthDelete removes metadata and keyring secrets for an auth identity.
func (c *Config) AuthDelete(subject, clusterID string, local bool) error {
	ref := AuthRef{Subject: subject, ClusterID: clusterID, Local: local}
	if err := c.authDeleteKey(authKey(ref)); err != nil {
		return fmt.Errorf("delete auth for subject %q on cluster %q: %w", subject, clusterID, err)
	}
	return nil
}

// authDeleteKey deletes auth metadata and secrets by their fully-qualified auth key.
func (c *Config) authDeleteKey(key string) error {
	// Remove from viper config.
	all := c.viperFile.GetStringMap("auth")
	if _, ok := all[key]; ok {
		delete(all, key)
		c.viperFile.Set("auth", all)
		if err := c.SaveFile(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
	}

	// Remove from keyring (ignore not-found).
	if err := c.KeyringDelete(keyringPrefix + key); err != nil {
		return fmt.Errorf("delete keyring for %s: %w", key, err)
	}
	return nil
}

// AuthListByCluster returns every AuthMetadata entry whose ClusterID matches.
func (c *Config) AuthListByCluster(clusterID string, local bool) ([]AuthMetadata, error) {
	out := []AuthMetadata{}
	all := c.viperFile.GetStringMap("auth")
	prefix := authKeyPrefix(local)
	for key, raw := range all {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var meta AuthMetadata
		if err := decodeMap(m, &meta); err != nil {
			return nil, fmt.Errorf("decode auth metadata for %s: %w", key, err)
		}
		if clusterID != "" && meta.ClusterID != clusterID {
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

// decodeMap converts a generic map[string]any (as returned by viper) into out.
// Uses a JSON round-trip to keep the dependency surface small.
func decodeMap(in map[string]any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
