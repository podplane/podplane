// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"path/filepath"
	"runtime"

	"github.com/99designs/keyring"
	"github.com/spf13/viper"
)

// Config provides getters/setters for working with the config, and holds
// our viper instance for the config file and keyring instance.
type Config struct {
	viperFile *viper.Viper
	keyring   *keyring.Keyring
}

// Init initializes the Config struct
func Init() (*Config, error) {
	c := &Config{
		viperFile: viper.New(),
	}
	err := c.InitFile()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// runtimeConfig defines the config variables, validation, and viper config
type runtimeConfig struct {
	Verbose       bool   `viper:"verbose" envkey:"PODPLANE_DEBUG" default:"false" json:"-" description:"Enable verbose output"`
	DepsBaseURL   string `viper:"deps_url" envkey:"PODPLANE_DEPS_URL" default:"https://deps.podplane.dev" json:"-" description:"Override the base URL for dependency manifests"`
	DepsCacheDir  string `viper:"deps_cache_dir" envkey:"PODPLANE_DEPS_CACHE_DIR" default:"" json:"-" description:"Override the directory to cache dependency files. Empty value defaults to CACHE_DIR/deps e.g. ~/.podplane/cache/deps"`
	Arch          string `viper:"arch" envkey:"PODPLANE_ARCH" default:"" json:"-" description:"Override the target architecture (arm64 or amd64). Empty value auto-detects from runtime.GOARCH."`
	OIDCIssuerURL string `viper:"oidc_issuer_url" envkey:"PODPLANE_OIDC_ISSUER_URL" default:"https://auth.podplane.dev" json:"-" description:"Override the Podplane Auth/OIDC Issuer URL"`
}

// Verbose returns true if debugging is enabled
func (c *Config) Verbose() bool {
	return viper.GetBool("verbose")
}

// Arch returns the target architecture for downloading deps. If the user has
// set PODPLANE_ARCH (or the equivalent config value), that takes precedence;
// otherwise it falls back to runtime.GOARCH (typically "arm64" or "amd64").
func (c *Config) Arch() string {
	if override := viper.GetString("arch"); override != "" {
		return override
	}
	return runtime.GOARCH
}

// InstanceKind returns the instance kind (always "knc" for now)
func (c *Config) InstanceKind() string {
	return "knc"
}

// OIDCIssuerURL returns Auth / OIDC Issuer URL
func (c *Config) OIDCIssuerURL() string {
	return viper.GetString("oidc_issuer_url")
}

// DepsBaseURL returns the URL used to fetch dependency manifests and artifacts.
func (c *Config) DepsBaseURL() string {
	url := viper.GetString("deps_url")
	if url != "" {
		return url
	}
	return "https://deps.podplane.dev"
}

// DepsCacheDir returns the directory used to cache deps files (the manifest
// JSON and all downloaded dependency artifacts).
func (c *Config) DepsCacheDir() string {
	configDir := viper.GetString("deps_cache_dir")
	if configDir != "" {
		return configDir
	}
	return filepath.Join(c.CacheDirectory(), "deps")
}
