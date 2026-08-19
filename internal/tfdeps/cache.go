// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfdeps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/podplane/podplane/internal/tfgen"
	"github.com/zclconf/go-cty/cty"
)

const (
	cacheDirName       = "tf"
	providersDirName   = "providers"
	modulesDirName     = "modules"
	nstanceDirName     = "nstance"
	lockFileName       = ".terraform.lock.hcl"
	currentVersionFile = "current-version"
	platformsFile      = "platforms"
	CLIConfigFileName  = "podplane.tfrc"
	clusterMainFile    = "podplane.cluster.main.tf"
)

var (
	platformPattern     = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)
	cachedModuleVersion = regexp.MustCompile(`/tf/modules/nstance/([^/"\\]+)/modules/cluster`)
)

// Engine performs the OpenTofu/Terraform operations needed to populate a cache.
type Engine interface {
	Platform(context.Context) (string, error)
	InitBackendDisabled(context.Context, string) error
	ProvidersMirror(context.Context, string, string, string) error
	ProvidersLock(context.Context, string, string, []string) error
}

// Cache manages the OpenTofu/Terraform dependency domain under the Podplane cache.
type Cache struct {
	root string
}

// Configuration describes the shared cache paths used by one cluster stack.
type Configuration struct {
	NstanceModuleDir string
	CLIConfigPath    string
}

// New returns an OpenTofu/Terraform dependency cache rooted below depsCacheDir.
func New(depsCacheDir string) *Cache {
	return &Cache{root: filepath.Join(depsCacheDir, cacheDirName)}
}

// Download resolves and caches all cluster Terraform dependencies.
func (c *Cache) Download(ctx context.Context, engine Engine, platforms []string) error {
	return c.download(ctx, engine, platforms, "", "", true)
}

// download resolves dependencies in staging and merges them into the shared cache.
func (c *Cache) download(ctx context.Context, engine Engine, platforms []string, moduleVersion, lockSeed string, updateCurrent bool) error {
	var err error
	platforms, err = resolvePlatforms(ctx, engine, platforms)
	if err != nil {
		return err
	}
	cachedPlatforms, err := c.cachedPlatforms()
	if err != nil {
		return err
	}
	platforms, err = resolvePlatforms(ctx, engine, append(platforms, cachedPlatforms...))
	if err != nil {
		return err
	}
	parent := filepath.Dir(c.root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create OpenTofu/Terraform dependency cache parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".tf-cache-")
	if err != nil {
		return fmt.Errorf("create OpenTofu/Terraform dependency cache staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	rootModule, err := os.MkdirTemp("", "podplane-tfdeps-")
	if err != nil {
		return fmt.Errorf("create temporary OpenTofu/Terraform root: %w", err)
	}
	defer func() { _ = os.RemoveAll(rootModule) }()
	if err := os.WriteFile(filepath.Join(rootModule, "main.tf"), []byte(tfgen.TerraformDependencyRoot(moduleVersion)), 0o644); err != nil {
		return fmt.Errorf("write temporary OpenTofu/Terraform root: %w", err)
	}
	if lockSeed != "" {
		if err := copyFile(lockSeed, filepath.Join(rootModule, lockFileName)); err != nil {
			return fmt.Errorf("seed provider lock information: %w", err)
		}
	}
	if err := engine.InitBackendDisabled(ctx, rootModule); err != nil {
		return err
	}

	mirrorDir := filepath.Join(stage, providersDirName)
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		return fmt.Errorf("create provider mirror: %w", err)
	}
	for _, platform := range platforms {
		if err := engine.ProvidersMirror(ctx, rootModule, mirrorDir, platform); err != nil {
			return err
		}
	}
	if err := engine.ProvidersLock(ctx, rootModule, mirrorDir, platforms); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(rootModule, lockFileName), filepath.Join(stage, lockFileName)); err != nil {
		return fmt.Errorf("retain provider lock information: %w", err)
	}
	sourceRoot, moduleVersion, err := nstanceSourceRoot(rootModule)
	if err != nil {
		return err
	}
	stagedModule := filepath.Join(stage, modulesDirName, nstanceDirName, moduleVersion)
	if err := copyTree(sourceRoot, stagedModule); err != nil {
		return fmt.Errorf("normalize Nstance module source: %w", err)
	}
	if err := mergeProviderMirror(mirrorDir, filepath.Join(c.root, providersDirName)); err != nil {
		return fmt.Errorf("merge provider mirror: %w", err)
	}
	nstanceModuleDir := filepath.Join(c.root, modulesDirName, nstanceDirName, moduleVersion)
	if err := installModuleVersion(stagedModule, nstanceModuleDir); err != nil {
		return fmt.Errorf("install Nstance module version %s: %w", moduleVersion, err)
	}
	if updateCurrent {
		if err := copyFileAtomic(filepath.Join(stage, lockFileName), filepath.Join(c.root, lockFileName)); err != nil {
			return fmt.Errorf("retain provider lock information: %w", err)
		}
		versionPath := filepath.Join(c.root, modulesDirName, nstanceDirName, currentVersionFile)
		if err := writeFileAtomic(versionPath, []byte(moduleVersion+"\n"), 0o644); err != nil {
			return fmt.Errorf("record current Nstance module version: %w", err)
		}
	}
	if err := writeFileAtomic(filepath.Join(c.root, platformsFile), []byte(strings.Join(platforms, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("record cached OpenTofu/Terraform platforms: %w", err)
	}
	if _, err := c.writeCLIConfig(); err != nil {
		return err
	}
	return nil
}

// cachedPlatforms returns every provider platform previously added to the cache.
func (c *Cache) cachedPlatforms() ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(c.root, platformsFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cached OpenTofu/Terraform platforms: %w", err)
	}
	return strings.Fields(string(raw)), nil
}

// Ensure prepares a cluster stack to use the shared cache, restoring exact
// dependencies recorded by existing generated files when they are missing.
func (c *Cache) Ensure(ctx context.Context, engine Engine, clusterDir string) (Configuration, error) {
	version, pinned, err := clusterModuleVersion(clusterDir)
	if err != nil {
		return Configuration{}, err
	}
	lockPath := filepath.Join(clusterDir, lockFileName)
	if pinned {
		if _, err := os.Stat(lockPath); err != nil {
			if os.IsNotExist(err) {
				return Configuration{}, fmt.Errorf("existing cluster stack is missing %s; cannot restore its exact provider versions", lockFileName)
			}
			return Configuration{}, fmt.Errorf("inspect cluster provider lock: %w", err)
		}
		platform, err := engine.Platform(ctx)
		if err != nil {
			return Configuration{}, err
		}
		available, err := c.dependenciesAvailable(version, lockPath, platform)
		if err != nil {
			return Configuration{}, err
		}
		if !available {
			if err := c.download(ctx, engine, []string{platform}, version, lockPath, false); err != nil {
				return Configuration{}, fmt.Errorf("restore cached OpenTofu/Terraform dependencies: %w", err)
			}
		}
		return c.configureVersion(clusterDir, version, true)
	}

	download := false
	version, err = c.currentModuleVersion()
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return Configuration{}, err
		}
		download = true
	} else {
		platform, err := engine.Platform(ctx)
		if err != nil {
			return Configuration{}, err
		}
		available, err := c.dependenciesAvailable(version, filepath.Join(c.root, lockFileName), platform)
		if err != nil {
			return Configuration{}, err
		}
		download = !available
	}
	if download {
		if err := c.Download(ctx, engine, nil); err != nil {
			return Configuration{}, err
		}
		version, err = c.currentModuleVersion()
	}
	if err != nil {
		return Configuration{}, err
	}
	return c.configureVersion(clusterDir, version, false)
}

// Upgrade downloads the latest supported dependencies and moves an existing
// cluster stack to those versions.
func (c *Cache) Upgrade(ctx context.Context, engine Engine, clusterDir string) (Configuration, error) {
	_, pinned, err := clusterModuleVersion(clusterDir)
	if err != nil {
		return Configuration{}, err
	}
	if !pinned {
		return Configuration{}, fmt.Errorf("cluster infrastructure has not been generated; run `podplane cluster create` first")
	}
	if err := c.Download(ctx, engine, nil); err != nil {
		return Configuration{}, err
	}
	version, err := c.currentModuleVersion()
	if err != nil {
		return Configuration{}, err
	}
	return c.configureVersion(clusterDir, version, false)
}

// currentModuleVersion returns the module version selected by the latest download.
func (c *Cache) currentModuleVersion() (string, error) {
	versionRaw, err := os.ReadFile(filepath.Join(c.root, modulesDirName, nstanceDirName, currentVersionFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("terraform dependencies are not cached; run `podplane deps tf` while online: %w", err)
		}
		return "", fmt.Errorf("read cached Nstance module version: %w", err)
	}
	return strings.TrimSpace(string(versionRaw)), nil
}

// configureVersion prepares a cluster to use one cached module version and the shared provider mirror.
func (c *Cache) configureVersion(clusterDir, version string, preserveLock bool) (Configuration, error) {
	nstanceModuleDir := filepath.Join(c.root, modulesDirName, nstanceDirName, version)
	providerDir := filepath.Join(c.root, providersDirName)
	requiredPaths := []string{providerDir, nstanceModuleDir}
	if !preserveLock {
		requiredPaths = append(requiredPaths, filepath.Join(c.root, lockFileName))
	}
	for _, path := range requiredPaths {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return Configuration{}, fmt.Errorf("OpenTofu/Terraform dependency cache is incomplete; run `podplane deps tf` while online")
			}
			return Configuration{}, fmt.Errorf("inspect OpenTofu/Terraform dependency cache: %w", err)
		}
	}
	if err := validateModuleVersion(nstanceModuleDir); err != nil {
		return Configuration{}, err
	}
	relativeNstanceModuleDir, err := filepath.Rel(clusterDir, nstanceModuleDir)
	if err != nil {
		return Configuration{}, fmt.Errorf("make shared Nstance module path relative to cluster: %w", err)
	}
	if !strings.HasPrefix(relativeNstanceModuleDir, ".") {
		relativeNstanceModuleDir = "." + string(filepath.Separator) + relativeNstanceModuleDir
	}
	configPath, err := c.writeCLIConfig()
	if err != nil {
		return Configuration{}, err
	}
	if !preserveLock {
		if err := copyFileAtomic(filepath.Join(c.root, lockFileName), filepath.Join(clusterDir, lockFileName)); err != nil {
			return Configuration{}, fmt.Errorf("write provider lock information: %w", err)
		}
	}
	return Configuration{
		NstanceModuleDir: filepath.ToSlash(relativeNstanceModuleDir),
		CLIConfigPath:    configPath,
	}, nil
}

// writeCLIConfig writes the shared OpenTofu/Terraform provider configuration.
func (c *Cache) writeCLIConfig() (string, error) {
	providerDir := filepath.Join(c.root, providersDirName)
	config := "provider_installation {\n  filesystem_mirror {\n    path = " + strconv.Quote(filepath.ToSlash(providerDir)) + "\n  }\n}\n"
	configPath := filepath.Join(c.root, CLIConfigFileName)
	existing, err := os.ReadFile(configPath)
	if err == nil && bytes.Equal(existing, []byte(config)) {
		return configPath, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read shared OpenTofu/Terraform CLI configuration: %w", err)
	}
	if err := writeFileAtomic(configPath, []byte(config), 0o644); err != nil {
		return "", fmt.Errorf("write shared OpenTofu/Terraform CLI configuration: %w", err)
	}
	return configPath, nil
}

// clusterModuleVersion reads the exact cached module version from generated Terraform.
func clusterModuleVersion(clusterDir string) (string, bool, error) {
	raw, err := os.ReadFile(filepath.Join(clusterDir, clusterMainFile))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read generated cluster Terraform: %w", err)
	}
	match := cachedModuleVersion.FindSubmatch(bytes.ReplaceAll(raw, []byte{'\\'}, []byte{'/'}))
	if len(match) == 0 {
		return "", false, nil
	}
	version := string(match[1])
	if version == "." || version == ".." || filepath.Base(version) != version {
		return "", false, fmt.Errorf("generated cluster Terraform has invalid cached module version %q", version)
	}
	return version, true, nil
}

// moduleVersionAvailable reports whether a complete cached module version exists.
func (c *Cache) moduleVersionAvailable(version string) bool {
	return validateModuleVersion(filepath.Join(c.root, modulesDirName, nstanceDirName, version)) == nil
}

// dependenciesAvailable reports whether a stack's exact dependencies exist for a platform.
func (c *Cache) dependenciesAvailable(moduleVersion, lockPath, platform string) (bool, error) {
	if !c.moduleVersionAvailable(moduleVersion) {
		return false, nil
	}
	if _, err := os.Stat(lockPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect provider lock: %w", err)
	}
	providers, err := readProviderLocks(lockPath)
	if err != nil {
		return false, err
	}
	for _, provider := range providers {
		parts := strings.Split(provider.address, "/")
		if len(parts) != 3 {
			return false, fmt.Errorf("provider lock has invalid address %q", provider.address)
		}
		filename := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", parts[2], provider.version, platform)
		path := filepath.Join(c.root, providersDirName, parts[0], parts[1], parts[2], filename)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("inspect cached provider package: %w", err)
		}
	}
	return len(providers) > 0, nil
}

type providerLock struct {
	address string
	version string
}

// readProviderLocks reads exact provider addresses and versions from a lock file.
func readProviderLocks(path string) ([]providerLock, error) {
	parser := hclparse.NewParser()
	file, diagnostics := parser.ParseHCLFile(path)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse provider lock %s: %s", path, diagnostics.Error())
	}
	content, _, diagnostics := file.Body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "provider", LabelNames: []string{"address"}}},
	})
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("read provider lock %s: %s", path, diagnostics.Error())
	}
	providers := make([]providerLock, 0, len(content.Blocks))
	for _, block := range content.Blocks {
		attributes, diagnostics := block.Body.JustAttributes()
		if diagnostics.HasErrors() {
			return nil, fmt.Errorf("read provider lock entry %q: %s", block.Labels[0], diagnostics.Error())
		}
		versionAttribute, ok := attributes["version"]
		if !ok {
			return nil, fmt.Errorf("provider lock entry %q has no version", block.Labels[0])
		}
		value, diagnostics := versionAttribute.Expr.Value(nil)
		if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
			return nil, fmt.Errorf("provider lock entry %q has invalid version", block.Labels[0])
		}
		providers = append(providers, providerLock{address: block.Labels[0], version: value.AsString()})
	}
	return providers, nil
}

// validateModuleVersion checks that every generated cluster submodule exists.
func validateModuleVersion(dir string) error {
	for _, name := range []string{"cluster", "account", "network", "shard"} {
		if info, err := os.Stat(filepath.Join(dir, "modules", name)); err != nil || !info.IsDir() {
			if err == nil {
				err = fmt.Errorf("not a directory")
			}
			return fmt.Errorf("cached Nstance module %s is incomplete: %w", name, err)
		}
	}
	return nil
}

// resolvePlatforms validates, deduplicates, and defaults provider platforms.
func resolvePlatforms(ctx context.Context, engine Engine, platforms []string) ([]string, error) {
	if len(platforms) == 0 {
		platform, err := engine.Platform(ctx)
		if err != nil {
			return nil, err
		}
		platforms = []string{platform}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		platform = strings.TrimSpace(platform)
		if !platformPattern.MatchString(platform) {
			return nil, fmt.Errorf("invalid OpenTofu/Terraform platform %q; expected OS_ARCH such as linux_amd64", platform)
		}
		if !seen[platform] {
			seen[platform] = true
			out = append(out, platform)
		}
	}
	return out, nil
}

// nstanceSourceRoot locates the resolved Nstance package root and exact version.
func nstanceSourceRoot(rootModule string) (string, string, error) {
	raw, err := os.ReadFile(filepath.Join(rootModule, ".terraform", "modules", "modules.json"))
	if err != nil {
		return "", "", fmt.Errorf("read resolved Terraform modules: %w", err)
	}
	var manifest struct {
		Modules []struct {
			Key     string `json:"Key"`
			Dir     string `json:"Dir"`
			Version string `json:"Version"`
		} `json:"Modules"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", "", fmt.Errorf("decode resolved Terraform modules: %w", err)
	}
	for _, module := range manifest.Modules {
		if module.Key != "cluster" {
			continue
		}
		dir := filepath.Join(rootModule, filepath.FromSlash(module.Dir))
		suffix := filepath.Join("modules", "cluster")
		if filepath.Base(dir) != "cluster" || filepath.Base(filepath.Dir(dir)) != "modules" {
			return "", "", fmt.Errorf("resolved Nstance cluster module has unexpected directory %q", module.Dir)
		}
		if module.Version == "" || filepath.Base(module.Version) != module.Version || module.Version == "." || module.Version == ".." {
			return "", "", fmt.Errorf("resolved Nstance module has invalid version %q", module.Version)
		}
		return strings.TrimSuffix(dir, suffix), module.Version, nil
	}
	return "", "", fmt.Errorf("resolved Nstance module source was not found")
}

// copyTree recursively copies src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copyFile(path, target)
	})
}

// mergeProviderMirror additively merges provider mirror files from src into dst.
func mergeProviderMirror(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.Link(path, target); err == nil || os.IsExist(err) {
			return nil
		} else {
			return err
		}
	})
}

// copyFile copies one file while preserving its permission bits.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// installModuleVersion atomically installs a staged module version when absent.
func installModuleVersion(stage, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return validateModuleVersion(dst)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(stage, dst)
}

// copyFileAtomic atomically copies src to dst.
func copyFileAtomic(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, raw, info.Mode().Perm())
}

// writeFileAtomic replaces path with content without exposing a partial file.
func writeFileAtomic(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
