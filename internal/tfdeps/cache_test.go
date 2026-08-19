// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfdeps

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeEngine struct {
	platforms       []string
	moduleVersion   string
	providerVersion string
	roots           []string
}

// Platform returns the fake engine's host platform.
func (e *fakeEngine) Platform(context.Context) (string, error) {
	return "darwin_arm64", nil
}

// InitBackendDisabled creates fake resolved modules and provider lock data.
func (e *fakeEngine) InitBackendDisabled(_ context.Context, dir string) error {
	if e.moduleVersion == "" {
		e.moduleVersion = "2.0.1"
	}
	if e.providerVersion == "" {
		e.providerVersion = "1.0.0"
	}
	root, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		return err
	}
	e.roots = append(e.roots, string(root))
	source := filepath.Join(dir, ".terraform", "modules", "cluster")
	if err := os.MkdirAll(filepath.Join(source, "modules", "cluster"), 0o755); err != nil {
		return err
	}
	for _, name := range []string{"account", "network", "shard"} {
		if err := os.MkdirAll(filepath.Join(source, "modules", name), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(source, "modules", "cluster", "main.tf"), []byte("cluster"), 0o644); err != nil {
		return err
	}
	manifest := `{"Modules":[{"Key":"","Dir":"."},{"Key":"cluster","Dir":".terraform/modules/cluster/modules/cluster","Version":"` + e.moduleVersion + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".terraform", "modules", "modules.json"), []byte(manifest), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, lockFileName)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeFakeLock(filepath.Join(dir, lockFileName), e.providerVersion)
}

// ProvidersMirror writes fake provider packages for one platform.
func (e *fakeEngine) ProvidersMirror(_ context.Context, dir, mirrorDir, platform string) error {
	e.platforms = append(e.platforms, platform)
	providers, err := readProviderLocks(filepath.Join(dir, lockFileName))
	if err != nil {
		return err
	}
	provider := providers[0]
	parts := strings.Split(provider.address, "/")
	providerDir := filepath.Join(mirrorDir, parts[0], parts[1], parts[2])
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		return err
	}
	name := "terraform-provider-" + parts[2] + "_" + provider.version + "_" + platform + ".zip"
	return os.WriteFile(filepath.Join(providerDir, name), []byte(platform), 0o644)
}

// ProvidersLock accepts the fake mirror's existing lock data.
func (e *fakeEngine) ProvidersLock(context.Context, string, string, []string) error {
	return nil
}

// writeFakeLock writes one provider selection for cache tests.
func writeFakeLock(path, version string) error {
	content := "provider \"registry.terraform.io/hashicorp/aws\" {\n  version = \"" + version + "\"\n  hashes = []\n}\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// TestDownloadAndConfigureSharedCache verifies additive shared-cache configuration.
func TestDownloadAndConfigureSharedCache(t *testing.T) {
	depsDir := t.TempDir()
	cache := New(depsDir)
	engine := &fakeEngine{moduleVersion: "2.0.1", providerVersion: "1.0.0"}
	if err := cache.Download(context.Background(), engine, []string{"linux_amd64", "linux_arm64", "linux_amd64"}); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if !reflect.DeepEqual(engine.platforms, []string{"linux_amd64", "linux_arm64"}) {
		t.Fatalf("mirrored platforms = %v", engine.platforms)
	}
	for _, path := range []string{
		filepath.Join(cache.root, providersDirName, "registry.terraform.io", "hashicorp", "aws", "terraform-provider-aws_1.0.0_linux_amd64.zip"),
		filepath.Join(cache.root, providersDirName, "registry.terraform.io", "hashicorp", "aws", "terraform-provider-aws_1.0.0_linux_arm64.zip"),
		filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.0.1", "modules", "cluster", "main.tf"),
		filepath.Join(cache.root, lockFileName),
		filepath.Join(cache.root, CLIConfigFileName),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cached path %s missing: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cache.root, ".terraform")); !os.IsNotExist(err) {
		t.Fatalf("cache retained temporary .terraform tree: %v", err)
	}

	firstClusterDir := t.TempDir()
	first, err := cache.Ensure(context.Background(), engine, firstClusterDir)
	if err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	if first.CLIConfigPath != filepath.Join(cache.root, CLIConfigFileName) {
		t.Fatalf("config path = %q", first.CLIConfigPath)
	}
	if got := filepath.Clean(filepath.Join(firstClusterDir, filepath.FromSlash(first.NstanceModuleDir))); got != filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.0.1") {
		t.Fatalf("first cluster module directory = %q", got)
	}
	config, err := os.ReadFile(first.CLIConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), filepath.ToSlash(filepath.Join(cache.root, providersDirName))) || strings.Contains(string(config), "direct") {
		t.Fatalf("unexpected CLI config:\n%s", config)
	}
	lock, err := os.ReadFile(filepath.Join(firstClusterDir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lock), `version = "1.0.0"`) {
		t.Fatalf("first cluster lock = %q", lock)
	}
	if _, err := os.Stat(filepath.Join(cache.root, providersDirName, "registry.terraform.io", "hashicorp", "aws", "terraform-provider-aws_1.0.0_darwin_arm64.zip")); err != nil {
		t.Fatalf("Ensure did not cache the engine platform: %v", err)
	}

	engine.moduleVersion = "2.1.0"
	engine.providerVersion = "1.1.0"
	if err := cache.Download(context.Background(), engine, []string{"linux_amd64"}); err != nil {
		t.Fatalf("second Download returned error: %v", err)
	}
	secondClusterDir := t.TempDir()
	second, err := cache.Ensure(context.Background(), engine, secondClusterDir)
	if err != nil {
		t.Fatalf("second Ensure returned error: %v", err)
	}
	if second.CLIConfigPath != first.CLIConfigPath {
		t.Fatalf("clusters use different CLI configs: %q and %q", first.CLIConfigPath, second.CLIConfigPath)
	}
	if got := filepath.Clean(filepath.Join(secondClusterDir, filepath.FromSlash(second.NstanceModuleDir))); got != filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.1.0") {
		t.Fatalf("second cluster module directory = %q", got)
	}
	for _, path := range []string{
		filepath.Join(cache.root, providersDirName, "registry.terraform.io", "hashicorp", "aws", "terraform-provider-aws_1.0.0_linux_amd64.zip"),
		filepath.Join(cache.root, providersDirName, "registry.terraform.io", "hashicorp", "aws", "terraform-provider-aws_1.1.0_linux_amd64.zip"),
		filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.0.1", "modules", "cluster"),
		filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.1.0", "modules", "cluster"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("versioned cache path %s missing: %v", path, err)
		}
	}
	firstLock, err := os.ReadFile(filepath.Join(firstClusterDir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstLock), `version = "1.0.0"`) {
		t.Fatalf("first cluster lock changed after cache update: %q", firstLock)
	}
}

// TestDownloadDefaultsToEnginePlatform verifies downloads default to the engine platform.
func TestDownloadDefaultsToEnginePlatform(t *testing.T) {
	cache := New(t.TempDir())
	engine := &fakeEngine{}
	if err := cache.Download(context.Background(), engine, nil); err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if !reflect.DeepEqual(engine.platforms, []string{"darwin_arm64"}) {
		t.Fatalf("mirrored platforms = %v", engine.platforms)
	}
}

// TestEnsureRestoresVersionsFromGeneratedSourceAndLock verifies exact historical recovery.
func TestEnsureRestoresVersionsFromGeneratedSourceAndLock(t *testing.T) {
	cache := New(t.TempDir())
	engine := &fakeEngine{moduleVersion: "2.0.1", providerVersion: "1.0.0"}
	if err := cache.Download(context.Background(), engine, []string{"darwin_arm64"}); err != nil {
		t.Fatal(err)
	}
	clusterDir := t.TempDir()
	configuration, err := cache.Ensure(context.Background(), engine, clusterDir)
	if err != nil {
		t.Fatal(err)
	}
	main := "module \"cluster\" {\n  source = \"" + configuration.NstanceModuleDir + "/modules/cluster\"\n}\n"
	if err := os.WriteFile(filepath.Join(clusterDir, clusterMainFile), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	engine.moduleVersion = "2.1.0"
	engine.providerVersion = "1.1.0"
	if err := cache.Download(context.Background(), engine, []string{"darwin_arm64"}); err != nil {
		t.Fatal(err)
	}
	oldModule := filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.0.1")
	oldProvider := filepath.Join(cache.root, providersDirName, "registry.terraform.io", "hashicorp", "aws", "terraform-provider-aws_1.0.0_darwin_arm64.zip")
	if err := os.RemoveAll(oldModule); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldProvider); err != nil {
		t.Fatal(err)
	}

	engine.moduleVersion = "2.0.1"
	engine.providerVersion = "9.9.9"
	restored, err := cache.Ensure(context.Background(), engine, clusterDir)
	if err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	if got := filepath.Clean(filepath.Join(clusterDir, filepath.FromSlash(restored.NstanceModuleDir))); got != oldModule {
		t.Fatalf("restored module directory = %q", got)
	}
	for _, path := range []string{oldModule, oldProvider} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("restored dependency %s missing: %v", path, err)
		}
	}
	current, err := cache.currentModuleVersion()
	if err != nil {
		t.Fatal(err)
	}
	if current != "2.1.0" {
		t.Fatalf("historical restore changed current module version to %q", current)
	}
	if got := engine.roots[len(engine.roots)-1]; strings.Count(got, `version = "2.0.1"`) != 4 {
		t.Fatalf("restore did not request exact module version:\n%s", got)
	}
	lock, err := os.ReadFile(filepath.Join(clusterDir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lock), `version = "1.0.0"`) {
		t.Fatalf("restore changed cluster provider lock:\n%s", lock)
	}
}

// TestUpgradeMovesCachedStackToCurrentVersions verifies explicit stack upgrades.
func TestUpgradeMovesCachedStackToCurrentVersions(t *testing.T) {
	cache := New(t.TempDir())
	engine := &fakeEngine{moduleVersion: "2.0.1", providerVersion: "1.0.0"}
	if err := cache.Download(context.Background(), engine, []string{"darwin_arm64"}); err != nil {
		t.Fatal(err)
	}
	clusterDir := t.TempDir()
	configuration, err := cache.Ensure(context.Background(), engine, clusterDir)
	if err != nil {
		t.Fatal(err)
	}
	main := "module \"cluster\" {\n  source = \"" + configuration.NstanceModuleDir + "/modules/cluster\"\n}\n"
	if err := os.WriteFile(filepath.Join(clusterDir, clusterMainFile), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	engine.moduleVersion = "2.1.0"
	engine.providerVersion = "1.1.0"
	upgraded, err := cache.Upgrade(context.Background(), engine, clusterDir)
	if err != nil {
		t.Fatalf("Upgrade returned error: %v", err)
	}
	if got := filepath.Clean(filepath.Join(clusterDir, filepath.FromSlash(upgraded.NstanceModuleDir))); got != filepath.Join(cache.root, modulesDirName, nstanceDirName, "2.1.0") {
		t.Fatalf("upgraded module directory = %q", got)
	}
	lock, err := os.ReadFile(filepath.Join(clusterDir, lockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lock), `version = "1.1.0"`) {
		t.Fatalf("upgraded provider lock:\n%s", lock)
	}
}

// TestUpgradeRequiresGeneratedInfrastructure verifies upgrades reject clusters without generated infrastructure.
func TestUpgradeRequiresGeneratedInfrastructure(t *testing.T) {
	_, err := New(t.TempDir()).Upgrade(context.Background(), &fakeEngine{}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "cluster create") {
		t.Fatalf("Upgrade error = %v", err)
	}
}

// TestReadProviderLocksRejectsNullVersion verifies malformed locks return an error rather than panic.
func TestReadProviderLocksRejectsNullVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	if err := os.WriteFile(path, []byte(`provider "registry.terraform.io/hashicorp/aws" { version = null }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readProviderLocks(path); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("readProviderLocks error = %v", err)
	}
}

// TestClusterModuleVersionRejectsTraversal verifies generated paths cannot escape the module cache.
func TestClusterModuleVersionRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	main := `module "cluster" { source = "../../deps/tf/modules/nstance/../modules/cluster" }`
	if err := os.WriteFile(filepath.Join(dir, clusterMainFile), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := clusterModuleVersion(dir); err == nil || !strings.Contains(err.Error(), "invalid cached module version") {
		t.Fatalf("clusterModuleVersion error = %v", err)
	}
}

// TestDownloadRejectsInvalidPlatform verifies provider platform validation.
func TestDownloadRejectsInvalidPlatform(t *testing.T) {
	err := New(t.TempDir()).Download(context.Background(), &fakeEngine{}, []string{"linux/amd64"})
	if err == nil || !strings.Contains(err.Error(), "expected OS_ARCH") {
		t.Fatalf("Download error = %v", err)
	}
}
