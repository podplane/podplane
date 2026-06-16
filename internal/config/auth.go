// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AuthMetadata is the per-(sub, cluster) metadata the CLI needs to refresh
// tokens and emit kubectl ExecCredentials.
type AuthMetadata struct {
	Sub         string `mapstructure:"sub" json:"sub"`
	ClusterID   string `mapstructure:"cluster_id" json:"cluster_id"`
	ClusterName string `mapstructure:"cluster_name" json:"cluster_name"`
	Issuer      string `mapstructure:"issuer" json:"issuer"`
	ClientID    string `mapstructure:"client_id" json:"client_id"`
	UserEmail   string `mapstructure:"user_email" json:"user_email"`
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

// authKey returns the config and keyring key for a user's cluster auth state.
func authKey(sub, clusterID string, local bool) string {
	if local {
		return "local:" + sub + ":" + clusterID
	}
	return sub + ":" + clusterID
}

// AuthGet returns the metadata + secrets for a given (sub, clusterID) pair.
// Returns zero values (and no error) if the entry does not exist.
func (c *Config) AuthGet(sub, clusterID string, local bool) (AuthMetadata, AuthSecrets, error) {
	key := authKey(sub, clusterID, local)
	meta, secrets, err := c.authGetKey(key)
	if err != nil || !local || meta.Sub != "" || secrets.IDToken != "" || secrets.RefreshToken != "" {
		return meta, secrets, err
	}
	// Local auth used to be stored under the unscoped remote key. Keep reading
	// that key so existing local clusters continue to work, but only write the
	// scoped key going forward. TODO: remove this pre-1.0 legacy fallback.
	return c.authGetKey(authKey(sub, clusterID, false))
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

// AuthSet writes both metadata (to the viper config) and secrets (to the
// keyring) for a given (sub, clusterID). The pair is taken from
// meta.{Sub,ClusterID}.
func (c *Config) AuthSet(meta AuthMetadata, secrets AuthSecrets, local bool) error {
	if meta.Sub == "" || meta.ClusterID == "" {
		return fmt.Errorf("AuthSet: sub and cluster_id are required")
	}
	key := authKey(meta.Sub, meta.ClusterID, local)

	// Write metadata to viper config.
	c.viperFile.Set("auth."+key, map[string]any{
		"sub":          meta.Sub,
		"cluster_id":   meta.ClusterID,
		"cluster_name": meta.ClusterName,
		"issuer":       meta.Issuer,
		"client_id":    meta.ClientID,
		"user_email":   meta.UserEmail,
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
		return fmt.Errorf("write keyring for %s: %w", key, err)
	}
	return nil
}

// AuthDelete removes both the metadata and the keyring secrets for the given
// (sub, clusterID) pair. Missing entries are a no-op.
func (c *Config) AuthDelete(sub, clusterID string, local bool) error {
	key := authKey(sub, clusterID, local)
	if err := c.authDeleteKey(key); err != nil {
		return err
	}
	if local {
		// Local auth used to be stored under the unscoped remote key. Delete it
		// while legacy fallback exists. TODO: remove this pre-1.0 legacy cleanup.
		return c.authDeleteKey(authKey(sub, clusterID, false))
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
		// keyringDelete may report "not found" as an error; we tolerate that.
		// We can't tell easily without backend-specific knowledge, so just
		// surface the error to the caller for visibility.
		return fmt.Errorf("delete keyring for %s: %w", key, err)
	}
	return nil
}

// AuthListByCluster returns every AuthMetadata entry whose ClusterID matches.
func (c *Config) AuthListByCluster(clusterID string, local bool) ([]AuthMetadata, error) {
	out := []AuthMetadata{}
	all := c.viperFile.GetStringMap("auth")
	for key, raw := range all {
		if strings.HasPrefix(key, "local:") != local {
			continue
		}
		if local {
			key = strings.TrimPrefix(key, "local:")
		}
		// key is "<sub>:<clusterID>" after local scope removal.
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 || parts[1] != clusterID {
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
		// Ensure sub/cluster_id are populated even if older config is missing them.
		if meta.Sub == "" {
			meta.Sub = parts[0]
		}
		if meta.ClusterID == "" {
			meta.ClusterID = parts[1]
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
