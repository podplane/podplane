// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// DefaultKind is the default instance kind used by the deps download command.
const DefaultKind = "knc"

// OS is the OS name encoded in the vmconfig manifest filename.
const OS = "debian-13"

// ImageDepName is the canonical name used for the OS image dependency. It is
// also used as the cache subdirectory for the image file (so the image lands
// at <depsCacheDir>/vmconfig/artifacts/os-image/<version>/<filename>).
const ImageDepName = "os-image"

// VMConfigDepName is the canonical dependency name for the vmconfig package
// tarball. Unreleased manifests generated in vmconfig/manifests contain a stub for
// this entry; release publishing fills in its URL and digest.
const VMConfigDepName = "vmconfig"

// Manifest is the top-level structure of the vmconfig manifest JSON file
// as published at
// <deps-url>/manifests/vmconfig_<kind>_<os>_<arch>.json.
type Manifest struct {
	VMConfig VMConfig `json:"vmconfig"`
}

// VMConfig is the inner manifest payload listing the OS image and all
// dependencies required to build a VM of the given kind.
type VMConfig struct {
	Version      string                `json:"version"`
	Kind         string                `json:"kind"`
	OS           OSInfo                `json:"os"`
	Dependencies map[string]Dependency `json:"dependencies"`
	Images       []VMConfigImage       `json:"images"`
}

// OSInfo describes the base OS, including the qcow2 image to download.
type OSInfo struct {
	Name  string     `json:"name"`
	Arch  string     `json:"arch"`
	Image Dependency `json:"image"`
	Boot  BootInfo   `json:"boot"`
}

// BootInfo describes explicit boot artifacts and the raw kernel command line
// extracted from the default boot entry for the OS image.
type BootInfo struct {
	Cmdline string       `json:"cmdline"`
	Kernel  BootArtifact `json:"kernel"`
	Initrd  BootArtifact `json:"initrd"`
}

// BootArtifact identifies a file inside the cached OS image.
type BootArtifact struct {
	Partition int    `json:"partition"`
	Path      string `json:"path"`
	Digest    string `json:"digest"`
}

// Complete reports whether the manifest has enough boot metadata for direct
// kernel boot. It validates only shape; extraction verifies file contents.
func (b BootInfo) Complete() bool {
	return b.Cmdline != "" && b.Kernel.Valid() && b.Initrd.Valid()
}

// Valid reports whether this boot artifact points at a specific image file.
func (b BootArtifact) Valid() bool {
	return b.Partition > 0 && b.Path != "" && b.Digest != ""
}

// Dependency describes a single downloadable artifact (binary, archive, deb,
// or qcow2 image) referenced by the manifest.
type Dependency struct {
	Version   string   `json:"version"`
	URL       string   `json:"url"`
	Type      string   `json:"type"`
	Digest    string   `json:"digest"`
	Size      int64    `json:"size"`
	Providers []string `json:"providers,omitempty"`
	Cached    bool     `json:"cached,omitempty"`
}

// VMConfigImage describes a runtime container image required by vmconfig
// itself, using the same shape as component/template image manifests.
type VMConfigImage struct {
	Image    string `json:"image"`
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	Platform string `json:"platform,omitempty"`
	Index    string `json:"index,omitempty"`
	Cached   bool   `json:"cached,omitempty"`
}

// vmconfigComponentImage adapts a vmconfig runtime image row to the shared
// registry mirror writer's ComponentImage input type.
func vmconfigComponentImage(image VMConfigImage) ComponentImage {
	return ComponentImage{
		Components: []string{"vmconfig"},
		Image:      image.Image,
		Digest:     image.Digest,
		Size:       image.Size,
		Platform:   image.Platform,
		Index:      image.Index,
	}
}

// ItemFilter selects manifest items by provider and cache state. An empty
// Providers list matches provider-neutral items only; "all" matches every
// provider-specific item.
type ItemFilter struct {
	Providers  []string
	CachedOnly bool
}

// Item pairs a dependency with its name (the OS image uses ImageDepName).
// It is the unit returned by Manifest.Items.
type Item struct {
	Name string
	Dep  Dependency
}

// Items returns the OS image plus every named dependency as a flat list, in
// no guaranteed order other than the OS image first.
func (m *Manifest) Items() []Item {
	items := make([]Item, 0, 1+len(m.VMConfig.Dependencies))
	items = append(items, Item{Name: ImageDepName, Dep: m.VMConfig.OS.Image})
	for name, dep := range m.VMConfig.Dependencies {
		items = append(items, Item{Name: name, Dep: dep})
	}
	return items
}

// ResetCached clears local cache-state markers from the OS image and all
// vmconfig dependencies.
func (m *Manifest) ResetCached() {
	m.VMConfig.OS.Image.Cached = false
	for name, dep := range m.VMConfig.Dependencies {
		dep.Cached = false
		m.VMConfig.Dependencies[name] = dep
	}
	for i := range m.VMConfig.Images {
		m.VMConfig.Images[i].Cached = false
	}
}

// MarkCached marks one vmconfig artifact entry as present in the local cache.
func (m *Manifest) MarkCached(name string) {
	if name == ImageDepName {
		m.VMConfig.OS.Image.Cached = true
		return
	}
	dep := m.VMConfig.Dependencies[name]
	dep.Cached = true
	m.VMConfig.Dependencies[name] = dep
}

// MarkImageCached marks one vmconfig runtime image entry as present in the
// local registry cache.
func (m *Manifest) MarkImageCached(index int) {
	if index < 0 || index >= len(m.VMConfig.Images) {
		return
	}
	m.VMConfig.Images[index].Cached = true
}

// DownloadItems returns only the artifacts the CLI can fetch and verify from
// the manifest. Local vmconfig/manifests manifests intentionally contain an
// unreleased vmconfig stub with no URL or digest; the release pipeline fills
// those fields for published manifests. The stub is not downloadable, so local
// development manifests skip it while still validating every real artifact.
func (m *Manifest) DownloadItems(filters ...ItemFilter) []Item {
	filter := itemFilterFromArgs(filters)
	items := m.Items()
	downloadItems := items[:0]
	for _, item := range items {
		if item.IsUnreleasedVMConfigStub() {
			continue
		}
		if !item.Matches(filter) {
			continue
		}
		downloadItems = append(downloadItems, item)
	}
	return downloadItems
}

// HasVMConfigDep reports whether the manifest includes a downloadable vmconfig
// package (i.e. it is present in the Dependencies map and is not an unreleased stub).
func (m *Manifest) HasVMConfigDep(filters ...ItemFilter) bool {
	filter := itemFilterFromArgs(filters)
	dep, ok := m.VMConfig.Dependencies[VMConfigDepName]
	if !ok {
		return false
	}
	it := Item{Name: VMConfigDepName, Dep: dep}
	return !it.IsUnreleasedVMConfigStub() && it.Matches(filter)
}

// InstallItems returns the dependencies that a running VM needs to install at
// boot time in deterministic order. This excludes the OS image (the VM is
// already running on it) and any unreleased vmconfig stubs. Stable ordering
// prevents semantically identical userdata from changing Nstance infra hashes.
func (m *Manifest) InstallItems(filters ...ItemFilter) []Item {
	filter := itemFilterFromArgs(filters)
	items := make([]Item, 0, len(m.VMConfig.Dependencies))
	names := make([]string, 0, len(m.VMConfig.Dependencies))
	for name := range m.VMConfig.Dependencies {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		dep := m.VMConfig.Dependencies[name]
		it := Item{Name: name, Dep: dep}
		if it.IsUnreleasedVMConfigStub() {
			continue
		}
		if !it.Matches(filter) {
			continue
		}
		items = append(items, it)
	}
	return items
}

func itemFilterFromArgs(filters []ItemFilter) ItemFilter {
	if len(filters) == 0 {
		return ItemFilter{}
	}
	return filters[0]
}

// Matches reports whether the item applies to the selected providers and cache
// state.
func (it Item) Matches(filter ItemFilter) bool {
	if filter.CachedOnly && !it.Dep.Cached {
		return false
	}
	if len(it.Dep.Providers) == 0 {
		return true
	}
	selected := map[string]bool{}
	for _, value := range filter.Providers {
		for _, provider := range strings.Split(value, ",") {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider == "" {
				continue
			}
			if provider == "all" {
				return true
			}
			selected[provider] = true
		}
	}
	for _, provider := range it.Dep.Providers {
		if selected[strings.ToLower(strings.TrimSpace(provider))] {
			return true
		}
	}
	return false
}

// IsUnreleasedVMConfigStub reports whether item is the placeholder vmconfig
// dependency found in checked-in vmconfig/manifests manifests before the release
// workflow substitutes the real tarball URL and digest.
func (it Item) IsUnreleasedVMConfigStub() bool {
	return it.Name == VMConfigDepName && it.Dep.URL == "" && it.Dep.Digest == ""
}

// digestHexLengths defines the expected hex-encoded length for each
// supported hash algorithm.
var digestHexLengths = map[string]int{
	"sha256": 64,
	"sha512": 128,
}

// ParseDigest returns the algorithm name and hex checksum from the Digest
// field. It returns an error if the digest is missing, malformed, or uses
// an unsupported algorithm. Supported algorithms: sha256, sha512.
func (d Dependency) ParseDigest() (algo string, hex string, err error) {
	if d.Digest == "" {
		return "", "", fmt.Errorf("missing digest")
	}
	parts := strings.SplitN(d.Digest, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid digest format %q (expected algo:hex)", d.Digest)
	}
	expectedLen, ok := digestHexLengths[parts[0]]
	if !ok {
		return "", "", fmt.Errorf("unsupported digest algorithm %q (supported: sha256, sha512)", parts[0])
	}
	if len(parts[1]) != expectedLen {
		return "", "", fmt.Errorf("invalid %s hex length in digest %q", parts[0], d.Digest)
	}
	return parts[0], parts[1], nil
}

// SHA256 returns the hex sha256 portion of the Digest. It returns an error
// if the digest is missing or uses any algorithm other than sha256.
// Deprecated: prefer ParseDigest which supports multiple algorithms.
func (d Dependency) SHA256() (string, error) {
	algo, hex, err := d.ParseDigest()
	if err != nil {
		return "", err
	}
	if algo != "sha256" {
		return "", fmt.Errorf("unsupported digest algorithm %q (only sha256 is supported)", algo)
	}
	return hex, nil
}

// Filename returns the base filename from the dependency URL (the last path
// segment), e.g. "runc" from "https://example.com/runc/v1.2.3/runc".
func (d Dependency) Filename() string {
	return path.Base(d.URL)
}

// LocalFilename returns the filename used when the dependency is placed in
// /opt/podplane/artifacts/ on the VM. The naming convention is <name>.<ext> where
// ext is derived from Type: binary has no extension, deb → .deb,
// tar.gz → .tar.gz.
func (it Item) LocalFilename() string {
	switch it.Dep.Type {
	case "deb":
		return it.Name + ".deb"
	case "tar.gz":
		return it.Name + ".tar.gz"
	default:
		return it.Name
	}
}

// DigestHex returns just the hex portion of the digest (strips the algo:
// prefix). Panics if digest is malformed — callers must validate first.
func (d Dependency) DigestHex() string {
	parts := strings.SplitN(d.Digest, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// ResolveURL returns the download URL for this item. When baseURL is
// non-empty (local dev, air-gapped mirrors) URLs are constructed as
// baseURL/vmconfig/artifacts/<name>/<version>/<filename>. Otherwise the original
// upstream URL from the manifest is used.
func (it Item) ResolveURL(baseURL string) string {
	if baseURL != "" {
		return baseURL + "/vmconfig/artifacts/" + it.Name + "/" + it.Dep.Version + "/" + it.Dep.Filename()
	}
	return it.Dep.URL
}
