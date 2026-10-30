// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/filecache"
)

// TestBundleImportRoundTrip exercises the public online-builder/offline-import
// contract with two guest architectures and multiple hosts sharing one arch.
func TestBundleImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	platforms := []string{"macos-arm64", "macos-amd64", "linux-arm64", "linux-amd64"}
	registryServer := httptest.NewServer(registry.New())
	defer registryServer.Close()
	ref, err := name.NewTag(strings.TrimPrefix(registryServer.URL, "http://") + "/example:v1")
	if err != nil {
		t.Fatal(err)
	}
	var index v1.ImageIndex = empty.Index
	var images []deps.ComponentImage
	var pinnedImages []deps.ComponentImage
	for _, arch := range []string{"arm64", "amd64"} {
		image, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{Architecture: arch, OS: "linux"})
		if err != nil {
			t.Fatal(err)
		}
		image, err = mutate.AppendLayers(image, static.NewLayer([]byte("layer-"+arch), types.OCIUncompressedLayer))
		if err != nil {
			t.Fatal(err)
		}
		digest, err := image.Digest()
		if err != nil {
			t.Fatal(err)
		}
		images = append(images, deps.ComponentImage{Image: ref.String(), Digest: digest.String(), Platform: "linux/" + arch, Size: 1})
		index = mutate.AppendManifests(index, mutate.IndexAddendum{Add: image, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: arch}}})
		// A distinct tag avoids a tag-only entry masking lost digest-pinned tags.
		pinned := ref.Context().Tag("direct-" + arch)
		if err := remote.Write(pinned, image); err != nil {
			t.Fatal(err)
		}
		pinnedImages = append(pinnedImages, deps.ComponentImage{Image: pinned.String() + "@" + digest.String(), Digest: digest.String(), Platform: "linux/" + arch, Size: 1})
	}
	if err := remote.WriteIndex(ref, index); err != nil {
		t.Fatal(err)
	}
	indexDigest, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	// Exercise both tag-only and upstream-index-pinned chart references.
	for _, image := range images[:2] {
		image.Image += "@" + indexDigest.String()
		images = append(images, image)
	}
	images = append(images, pinnedImages...)
	var base string
	requests := map[string]int{}
	var requestsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsMu.Lock()
		requests[r.URL.Path]++
		requestsMu.Unlock()
		write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		name := filepath.Base(r.URL.Path)
		switch {
		case strings.HasPrefix(name, "vmconfig_"):
			arch := strings.TrimSuffix(strings.TrimPrefix(name, "vmconfig_knc_debian-13_"), ".json")
			body := []byte("image-" + arch)
			binary := []byte("kube-apiserver-" + arch)
			write(deps.Manifest{VMConfig: deps.VMConfig{
				Kind: "knc", OS: deps.OSInfo{Arch: arch, Image: deps.Dependency{URL: base + "/image-" + arch, Version: "1", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)), Size: int64(len(body))}},
				// Both architectures have the same version and URL basename.
				Dependencies: map[string]deps.Dependency{"kube-apiserver": {URL: base + "/bin/" + arch + "/kube-apiserver", Version: "1", Digest: fmt.Sprintf("sha512:%x", sha512.Sum512(binary)), Size: int64(len(binary))}},
			}})
		case name == "kube-apiserver":
			_, _ = w.Write([]byte("kube-apiserver-" + filepath.Base(filepath.Dir(r.URL.Path))))
		case name == "components.json":
			write(deps.ComponentsManifest{Components: deps.Components{Version: "1", Images: images}})
		case name == "templates.json":
			write(deps.TemplatesManifest{Templates: deps.Templates{Version: "1"}})
		case name == "seeds.json":
			write(deps.SeedsManifest{Seeds: deps.Seeds{Version: "1", Components: "1", Snapshots: map[string]deps.SeedSnapshot{"agentic": {URL: base + "/agentic.netsy", Digest: fmt.Sprintf("sha512:%x", sha512.Sum512([]byte("seed"))), Size: 4}}}})
		case name == "agentic.netsy":
			_, _ = w.Write([]byte("seed"))
		case strings.HasPrefix(name, "image-"):
			_, _ = w.Write([]byte(name))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	output := filepath.Join(t.TempDir(), "USB with spaces")
	m := deps.NewManager(base, filepath.Join(t.TempDir(), "personal-cache"))
	// An ordinary deps download must seed bundle preparation, rather than
	// downloading the same guest bytes into another isolated cache.
	for i, arch := range []string{"arm64", "amd64"} {
		if err := m.Download("knc", arch, deps.DownloadOptions{Archs: []string{"arm64", "amd64"}, SkipCrossArchDependencies: i > 0}); err != nil {
			t.Fatal(err)
		}
	}
	toolSources := testBundleToolSources(t)
	toolRequests := map[string]int{}
	offline := false
	client := &http.Client{Transport: bundleTestTransport(func(r *http.Request) (*http.Response, error) {
		if offline {
			return nil, fmt.Errorf("network disabled: %s", r.URL)
		}
		if body, ok := toolSources[r.URL.String()]; ok {
			toolRequests[r.URL.String()]++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
		}
		if strings.HasPrefix(r.URL.String(), base) {
			return http.DefaultTransport.RoundTrip(r)
		}
		return nil, fmt.Errorf("unexpected download: %s", r.URL)
	})}
	opts := Options{Platforms: platforms, CacheDir: t.TempDir(), Client: client, Cleanup: true}
	copyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	states := map[string]deps.DownloadEvent{}
	var statesMu sync.Mutex
	opts.Progress = func(event deps.DownloadEvent) {
		statesMu.Lock()
		defer statesMu.Unlock()
		if event.Name != "" {
			states[event.Name] = event
		}
		if event.Name == "Archive podplane-linux-amd64" && event.Type == deps.DownloadEventProgress {
			cancel() // Interrupt archiving, not downloads or preparation.
		}
	}
	stage, err := Create(copyCtx, m, output, "knc", opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted archive = %v; want cancellation after preparation", err)
	}
	for name, event := range states {
		if name == "Archive podplane-linux-amd64" {
			if event.Type != deps.DownloadEventFailed || !errors.Is(event.Err, context.Canceled) {
				t.Fatalf("cancelled archive left stale progress: %+v", event)
			}
		} else if event.Type != deps.DownloadEventDone && event.Type != deps.DownloadEventCached {
			t.Fatalf("completed download left stale progress: %+v", event)
		}
	}
	if stage != filepath.Join(opts.CacheDir, "bundles") {
		t.Fatalf("staging is not the single local bundles directory: %s", stage)
	}
	if _, err := os.Stat(filepath.Join(stage, "ready")); err != nil {
		t.Fatalf("failed archive deleted prepared staging: %v", err)
	}
	if entries, err := os.ReadDir(output); err != nil || len(entries) != 0 {
		t.Fatalf("interrupted archive left files in the destination: %v, %v", entries, err)
	}
	offline = true
	opts.Cleanup = false
	states = map[string]deps.DownloadEvent{}
	opts.Progress = func(event deps.DownloadEvent) {
		statesMu.Lock()
		defer statesMu.Unlock()
		if event.Name != "" {
			states[event.Name] = event
		}
	}
	if _, err := Create(ctx, m, output, "knc", opts); err != nil {
		t.Fatalf("offline archive resume failed: %v", err)
	}
	if len(states) != len(platforms) {
		t.Fatalf("archiving should report one row per platform, got %d", len(states))
	}
	for _, platform := range platforms {
		event := states["Archive podplane-"+platform]
		if event.Type != deps.DownloadEventDone || event.Total <= 0 || event.Current != event.Total {
			t.Fatalf("archive did not complete its byte progress: %+v", event)
		}
	}
	// Rerunning against the same destination never replaces an archive.
	first := filepath.Join(output, "podplane-linux-amd64.tar.gz")
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, m, output, "knc", opts); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rerun accepted an existing archive: %v", err)
	}
	if after, err := os.ReadFile(first); err != nil || !bytes.Equal(before, after) {
		t.Fatal("rerun changed an existing archive")
	}
	changed := opts
	changed.Platforms = []string{"macos-arm64"}
	if _, err := Create(ctx, m, t.TempDir(), "knc", changed); err == nil || !strings.Contains(err.Error(), "--refresh") {
		t.Fatalf("changed selection reused incompatible staging: %v", err)
	}
	// Never overwrite an unrelated directory just because its name matches.
	unrelated := t.TempDir()
	sentinel := filepath.Join(unrelated, "podplane-linux-amd64.tar.gz")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, m, unrelated, "knc", opts); err == nil {
		t.Fatal("accepted unrelated destination")
	}
	if body, _ := os.ReadFile(sentinel); string(body) != "untouched" {
		t.Fatal("changed unrelated destination")
	}
	// Another USB uses exactly the same prepared bytes, without checking latest.
	second := t.TempDir()
	opts.Cleanup = true
	if _, err := Create(ctx, m, second, "knc", opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("explicit cleanup retained staging")
	}
	// Rebuilding staging rechecks upstream metadata, but reuses all downloads.
	counts := map[string]int{}
	for url, count := range toolRequests {
		counts[url] = count
	}
	offline = false
	opts.Cleanup = false
	if _, err := Create(ctx, m, t.TempDir(), "knc", opts); err != nil {
		t.Fatal(err)
	}
	for url, count := range counts {
		if strings.HasPrefix(url, "https://assets.example/") && toolRequests[url] != count {
			t.Fatalf("cached host artifact redownloaded after staging cleanup: %s", url)
		}
	}
	opts.Refresh = true
	if _, err := Create(ctx, m, t.TempDir(), "knc", opts); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "amd64"} {
		if got := requests["/bin/"+arch+"/kube-apiserver"]; got != 1 {
			t.Fatalf("%s artifact downloaded %d times across rebuilds, want 1", arch, got)
		}
	}
	server.Close() // Import must work after every source is unavailable.
	registryServer.Close()
	if requests["/agentic.netsy"] != 1 || requests["/image-arm64"] != 1 || requests["/image-amd64"] != 1 {
		t.Fatalf("shared artifacts fetched more than once: %v", requests)
	}
	for _, platform := range platforms {
		arch, _ := platformArch(platform)
		archive := filepath.Join(output, "podplane-"+platform+".tar.gz")
		kit := testUntar(t, archive)
		if err := os.Remove(archive); err != nil { // Model ejecting the original drive.
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(kit, "podplane-bundle.json"))
		if err != nil {
			t.Fatal(err)
		}
		var inventory Manifest
		if err := json.Unmarshal(raw, &inventory); err != nil {
			t.Fatal(err)
		}
		if inventory.CreatedAt.IsZero() || inventory.Platform != platform || inventory.Tools["podplane"] != "v1.2.3" || inventory.Tools["mkcert"] != "v1.4.4" {
			t.Fatalf("incorrect bundle metadata: %+v", inventory)
		}
		if inventory.Format != 2 || len(inventory.ContentsSHA256) != 64 || bytes.Contains(raw, []byte(`"files"`)) {
			t.Fatalf("bundle should contain one checksum, not a file listing: %s", raw)
		}
		if platform == "macos-amd64" {
			if _, bundled := inventory.Tools["qemu"]; bundled {
				t.Fatal("Intel Mac inventory claims QEMU is bundled")
			}
			files, err := filepath.Glob(filepath.Join(kit, "bin", "qemu-*"))
			if err != nil || len(files) != 0 {
				t.Fatalf("Intel Mac bundle includes QEMU: %v, %v", files, err)
			}
		} else {
			target := "aarch64"
			if arch == "amd64" {
				target = "x86_64"
			}
			for binary, suffix := range map[string]string{"qemu-img": "img", "qemu-system-" + target: "qemu"} {
				body, err := os.ReadFile(filepath.Join(kit, "bin", binary))
				if err != nil || string(body) != strings.Replace(platform, "macos-", "darwin-", 1)+":"+suffix {
					t.Fatalf("wrong %s binary in %s: %q, %v", binary, platform, body, err)
				}
			}
		}
		goPlatform := strings.Replace(platform, "macos-", "darwin-", 1)
		for _, binary := range []string{"podplane", "helm", "mkcert", "kubectl"} {
			body, err := os.ReadFile(filepath.Join(kit, "bin", binary))
			if err != nil || string(body) != goPlatform+":"+binary {
				t.Fatalf("wrong %s binary in %s: %q, %v", binary, platform, body, err)
			}
		}
		dest := deps.NewManager("http://127.0.0.1:1", filepath.Join(t.TempDir(), "deps"))
		for range 2 {
			states = map[string]deps.DownloadEvent{}
			if err := Import(ctx, dest, kit, "knc", arch, opts.Progress); err != nil {
				t.Fatal(err)
			}
			event := states["Import podplane-"+platform]
			if len(states) != 1 || event.Type != deps.DownloadEventDone || event.Current != event.Total || event.Total <= 0 {
				t.Fatalf("import should complete one aggregate row: %+v", states)
			}
		}
		manifest, err := dest.VerifyOffline("knc", arch, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, image := range images {
			cached, err := imageCached(dest.RegistryCacheDir(), image)
			if image.Platform == "linux/"+arch && (err != nil || !cached) {
				t.Fatalf("missing imported image %s (%s): %v", image.Image, arch, err)
			}
			if image.Platform != "linux/"+arch && cached {
				t.Fatalf("export included unwanted image architecture: %s", image.Platform)
			}
		}
		body, err := os.ReadFile(dest.VMConfigArtifactCachePath(deps.ImageDepName, manifest.VMConfig.OS.Image))
		if err != nil || string(body) != "image-"+arch {
			t.Fatalf("wrong guest artifact: %q, %v", body, err)
		}
		body, err = os.ReadFile(dest.VMConfigArtifactCachePath("kube-apiserver", manifest.VMConfig.Dependencies["kube-apiserver"]))
		if err != nil || string(body) != "kube-apiserver-"+arch {
			t.Fatalf("shared filename contains the wrong architecture: %q, %v", body, err)
		}
		other := "amd64"
		if arch == other {
			other = "arm64"
		}
		if _, err := os.Stat(filepath.Join(kit, "deps/vmconfig/artifacts/os-image/1/image-"+other)); !os.IsNotExist(err) {
			t.Fatal("kit includes the other guest architecture")
		}
	}
}

// testUntar extracts a bundle archive with the system tar, as recipients do,
// and returns its podplane-<platform> directory.
func testUntar(t *testing.T, archive string) string {
	t.Helper()
	directory := t.TempDir()
	if out, err := exec.Command("tar", "-xzf", archive, "-C", directory).CombinedOutput(); err != nil {
		t.Fatalf("tar -xzf %s: %v: %s", archive, err, out)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name()+".tar.gz" != filepath.Base(archive) {
		t.Fatalf("archive should contain one top-level directory named after it: %v, %v", entries, err)
	}
	return filepath.Join(directory, entries[0].Name())
}

// bundleTestTransport serves fixture responses through an HTTP client.
type bundleTestTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates a request to the fixture handler.
func (f bundleTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// testToolsArchive packages executable fixture files into a gzip-compressed tar archive.
func testToolsArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		body := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testBundleToolSources provides release metadata, archives and checksums for
// all supported host platforms without contacting upstream services.
func testBundleToolSources(t *testing.T) map[string][]byte {
	t.Helper()
	sources := map[string][]byte{"https://dl.k8s.io/release/stable.txt": []byte("v1.35.0\n")}
	releases := map[string][]map[string]string{}
	checksums := ""
	add := func(repo, name string, body []byte) {
		url := "https://assets.example/" + name
		sources[url] = body
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
		if repo == "FiloSottile/mkcert" {
			digest = "" // Older releases have no GitHub asset digest.
		}
		releases[repo] = append(releases[repo], map[string]string{"name": name, "browser_download_url": url, "digest": digest})
	}
	for _, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"} {
		goos, arch, _ := strings.Cut(platform, "-")
		name := "podplane_1.2.3_" + goos + "_" + arch + ".tar.gz"
		body := testToolsArchive(t, map[string]string{"podplane": platform + ":podplane", "LICENSE": "license"})
		add("podplane/podplane", name, body)
		checksums += fmt.Sprintf("%x  %s\n", sha512.Sum512(body), name)
		url := "https://dl.k8s.io/release/v1.35.0/bin/" + goos + "/" + arch + "/kubectl"
		sources[url] = []byte(platform + ":kubectl")
		sources[url+".sha256"] = []byte(fmt.Sprintf("%x", sha256.Sum256(sources[url])))
		url = "https://get.helm.sh/helm-v3.19.0-" + platform + ".tar.gz"
		sources[url] = testToolsArchive(t, map[string]string{platform + "/helm": platform + ":helm", platform + "/LICENSE": "license"})
		sources[url+".sha256sum"] = []byte(fmt.Sprintf("%x  helm.tar.gz", sha256.Sum256(sources[url])))
		add("FiloSottile/mkcert", "mkcert-v1.4.4-"+platform, []byte(platform+":mkcert"))
		if platform == "darwin-amd64" {
			continue
		}
		target := "aarch64"
		if arch == "amd64" {
			target = "x86_64"
		}
		if goos == "darwin" {
			add("jzombie/lab-qemu-portable", "qemu-portable-macos-arm64-11.1.2.tar.gz", testToolsArchive(t, map[string]string{
				"qemu-portable/bin/qemu-img": platform + ":img", "qemu-portable/bin/qemu-system-aarch64": platform + ":qemu", "qemu-portable/share/qemu/edk2-aarch64-code.fd": "firmware",
			}))
		} else {
			add("hermeticbuild/qemu-prebuilt", "qemu-img-linux-"+arch+"-11.0.0.1.tar.gz", testToolsArchive(t, map[string]string{"./bin/qemu-img": platform + ":img"}))
			add("hermeticbuild/qemu-prebuilt", "qemu-system-bin-linux-"+arch+"-"+target+"-softmmu-11.0.0.1.tar.gz", testToolsArchive(t, map[string]string{"./bin/qemu-system-" + target: platform + ":qemu"}))
			add("hermeticbuild/qemu-prebuilt", "qemu-system-data-linux-"+arch+"-11.0.0.1.tar.gz", testToolsArchive(t, map[string]string{"./share/qemu/edk2-" + target + "-code.fd": "firmware"}))
		}
	}
	add("podplane/podplane", "podplane_1.2.3_checksums.txt", []byte(checksums))
	for repo, tag := range map[string]string{"podplane/podplane": "v1.2.3", "helm/helm": "v3.19.0", "FiloSottile/mkcert": "v1.4.4", "hermeticbuild/qemu-prebuilt": "11.0.0.1", "jzombie/lab-qemu-portable": "qemu-v11.1.2-49"} {
		var value any = map[string]any{"tag_name": tag, "assets": releases[repo]}
		endpoint := "https://api.github.com/repos/" + repo + "/releases/latest"
		if repo == "jzombie/lab-qemu-portable" {
			value = []any{map[string]any{"tag_name": "drivers-20261026"}, value}
			endpoint = "https://api.github.com/repos/" + repo + "/releases?per_page=100"
		}
		sources[endpoint], _ = json.Marshal(value)
	}
	return sources
}

// TestBundleRejectsCorruptUpstreamTool checks that a digest mismatch retains
// staging and leaves the destination untouched, even with cleanup requested.
func TestBundleRejectsCorruptUpstreamTool(t *testing.T) {
	sources := testBundleToolSources(t)
	sources["https://assets.example/podplane_1.2.3_linux_amd64.tar.gz"] = []byte("corrupted archive")
	client := &http.Client{Transport: bundleTestTransport(func(r *http.Request) (*http.Response, error) {
		body, ok := sources[r.URL.String()]
		if !ok {
			return nil, fmt.Errorf("unexpected request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	cache, dest := t.TempDir(), t.TempDir()
	m := deps.NewManager("http://127.0.0.1:1", filepath.Join(cache, "deps"))
	stage, err := Create(context.Background(), m, dest, "knc", Options{Platforms: []string{"linux-amd64"}, CacheDir: cache, Client: client, Cleanup: true})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corrupt tool was accepted: %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("failed preparation lost staging: %v", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatal("failed preparation wrote to the destination")
	}
}

// testImportKit builds realistic tiny OCI images without network registries.
func testImportKit(t *testing.T, arch string) (string, deps.ComponentImage) {
	t.Helper()
	kit := t.TempDir()
	m := deps.NewManager("", filepath.Join(kit, "deps"))
	put := func(filename string, body []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte("os-" + arch)
	dep := deps.Dependency{URL: "https://example.invalid/os-" + arch, Version: "1", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(body)), Size: int64(len(body))}
	put(m.VMConfigArtifactCachePath(deps.ImageDepName, dep), body)
	layer := []byte("layer-" + arch)
	config := []byte(`{"architecture":"` + arch + `","os":"linux"}`)
	imageBody := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"sha256:%x","size":%d},"layers":[{"digest":"sha256:%x","size":%d}]}`, sha256.Sum256(config), len(config), sha256.Sum256(layer), len(layer)))
	image := deps.ComponentImage{Image: "example.invalid/manifests/test:v1", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(imageBody)), Platform: "linux/" + arch, Size: int64(len(imageBody))}
	repo := filepath.Join(m.RegistryCacheDir(), "mirror/example.invalid/manifests/test")
	for _, data := range [][]byte{layer, config, imageBody} {
		put(filepath.Join(repo, "blobs/sha256", fmt.Sprintf("%x", sha256.Sum256(data))), data)
	}
	digest, err := v1.NewHash(image.Digest)
	if err != nil {
		t.Fatal(err)
	}
	desc := v1.Descriptor{MediaType: types.OCIManifestSchema1, Digest: digest, Size: image.Size, Platform: &v1.Platform{OS: "linux", Architecture: arch}}
	index := v1.IndexManifest{SchemaVersion: 2, MediaType: types.OCIImageIndex, Manifests: []v1.Descriptor{desc}}
	indexBody, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	indexHash := fmt.Sprintf("%x", sha256.Sum256(indexBody))
	put(filepath.Join(repo, "blobs/sha256", indexHash), indexBody)
	index.Manifests = append(index.Manifests, v1.Descriptor{
		MediaType: types.OCIImageIndex, Digest: v1.Hash{Algorithm: "sha256", Hex: indexHash}, Size: int64(len(indexBody)),
		Annotations: map[string]string{"org.opencontainers.image.ref.name": "v1"},
	})
	put(filepath.Join(repo, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`))
	if err := filecache.WriteJSON(filepath.Join(repo, "index.json"), index); err != nil {
		t.Fatal(err)
	}
	for filename, value := range map[string]any{
		m.VMConfigManifestCachePath("knc", arch): deps.Manifest{VMConfig: deps.VMConfig{OS: deps.OSInfo{Arch: arch, Image: dep}}},
		m.ComponentsManifestCachePath():          deps.ComponentsManifest{Components: deps.Components{Version: "1", Images: []deps.ComponentImage{image}}},
		m.TemplatesManifestCachePath():           deps.TemplatesManifest{Templates: deps.Templates{Version: "1"}},
		m.SeedsManifestCachePath():               deps.SeedsManifest{Seeds: deps.Seeds{Version: "1"}},
	} {
		if err := filecache.WriteJSON(filename, value); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(kit)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	files, err := contentsInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := filecache.WriteJSON(filepath.Join(kit, "podplane-bundle.json"), Manifest{Format: format, Platform: "linux-" + arch, PodplaneVersion: "v1.2.3", Kind: "knc", Arch: arch, ContentsSHA256: contentsChecksum(files)}); err != nil {
		t.Fatal(err)
	}
	return kit, image
}

// TestImportPreservesOtherArchitecturesAndRejectsCorruption checks that imports
// retain images and reject damage before publishing manifests.
func TestImportPreservesOtherArchitecturesAndRejectsCorruption(t *testing.T) {
	ctx := context.Background()
	armKit, arm := testImportKit(t, "arm64")
	amdKit, amd := testImportKit(t, "amd64")
	dest := deps.NewManager("http://127.0.0.1:1", filepath.Join(t.TempDir(), "custom-cache"))
	if err := Import(ctx, dest, amdKit, "knc", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	if err := Import(ctx, dest, armKit, "knc", "arm64", nil); err != nil {
		t.Fatal(err)
	}
	for _, image := range []deps.ComponentImage{amd, arm} {
		if ok, err := imageCached(dest.RegistryCacheDir(), image); err != nil || !ok {
			t.Fatalf("lost image %s: %v", image.Platform, err)
		}
	}
	before, err := os.ReadFile(dest.ComponentsManifestCachePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(armKit, "deps/vmconfig/artifacts/os-image/1/os-arm64"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Import(ctx, dest, armKit, "knc", "arm64", nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corruption accepted: %v", err)
	}
	after, err := os.ReadFile(dest.ComponentsManifestCachePath())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed import changed active manifest: %v", err)
	}
}

// TestCompactBundleIntegrity checks format 2's whole-tree integrity and stable
// executable requirements across filesystems with different permission bits.
func TestCompactBundleIntegrity(t *testing.T) {
	for _, scenario := range []string{"valid", "permissions", "finder-metadata", "changed", "missing", "extra", "renamed", "symlink", "directory-symlink", "bad-checksum"} {
		t.Run(scenario, func(t *testing.T) {
			directory, _ := testImportKit(t, "arm64")
			binary := filepath.Join(directory, "bin/tool")
			if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("tool"), 0755); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			files, err := contentsInventory(root)
			if err != nil {
				t.Fatal(err)
			}
			manifest := Manifest{Format: 2, Platform: "linux-arm64", Kind: "knc", Arch: "arm64", ContentsSHA256: contentsChecksum(files)}
			switch scenario {
			case "finder-metadata":
				for _, name := range []string{".DS_Store", "bin/.DS_Store", "bin/._tool"} {
					if err := os.WriteFile(filepath.Join(directory, name), []byte("Finder metadata"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			case "permissions":
				for _, file := range files {
					mode := os.FileMode(0755)
					if file.Executable {
						mode = 0644
					}
					if err := os.Chmod(filepath.Join(directory, file.Path), mode); err != nil {
						t.Fatal(err)
					}
				}
			case "changed":
				err = os.WriteFile(binary, []byte("evil"), 0755) // Same size, different bytes.
			case "missing":
				err = os.Remove(binary)
			case "extra":
				err = os.WriteFile(filepath.Join(directory, "bin/extra"), []byte("tool"), 0755)
			case "renamed":
				err = os.Rename(binary, filepath.Join(directory, "bin/renamed"))
			case "symlink":
				if err = os.Remove(binary); err == nil {
					err = os.Symlink("/etc/hosts", binary)
				}
			case "directory-symlink":
				err = os.Symlink(t.TempDir(), filepath.Join(directory, "share"))
			case "bad-checksum":
				manifest.ContentsSHA256 = "not a SHA-256 digest"
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := filecache.WriteJSON(filepath.Join(directory, manifestFilename), manifest); err != nil {
				t.Fatal(err)
			}
			dest := deps.NewManager("http://127.0.0.1:1", filepath.Join(t.TempDir(), "deps"))
			err = Import(context.Background(), dest, directory, "knc", "arm64", nil)
			if scenario == "valid" || scenario == "permissions" || scenario == "finder-metadata" {
				if err != nil {
					t.Fatal(err)
				}
				archives := t.TempDir()
				if err := archivePrepared(context.Background(), directory, archives, nil); err != nil {
					t.Fatal(err)
				}
				copy := testUntar(t, filepath.Join(archives, "podplane-linux-arm64.tar.gz"))
				for _, file := range files {
					info, err := os.Stat(filepath.Join(copy, file.Path))
					if err != nil || (info.Mode()&0111 != 0) != file.Executable {
						t.Fatalf("incorrect copied permissions for %s: %v", file.Path, err)
					}
				}
				for _, name := range []string{".DS_Store", "bin/.DS_Store", "bin/._tool"} {
					if _, err := os.Stat(filepath.Join(copy, name)); !os.IsNotExist(err) {
						t.Fatalf("copied Finder metadata: %s", name)
					}
				}
				return
			}
			if err == nil {
				t.Fatal("damaged compact bundle accepted")
			}
			if _, err := os.Stat(dest.CacheDir()); !os.IsNotExist(err) {
				t.Fatal("invalid bundle modified the destination cache")
			}
		})
	}
}

// TestImportRejectsIncompatibleMetadata checks that incompatible bundles and
// missing checksums cannot publish a cache manifest.
func TestImportRejectsIncompatibleMetadata(t *testing.T) {
	for _, scenario := range []string{"arch", "format", "old-format", "missing-checksum"} {
		t.Run(scenario, func(t *testing.T) {
			kit, _ := testImportKit(t, "arm64")
			raw, _ := os.ReadFile(filepath.Join(kit, "podplane-bundle.json"))
			var manifest Manifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "arch":
				manifest.Arch = "amd64"
			case "format":
				manifest.Format = 999
			case "old-format":
				manifest.Format = 1
			case "missing-checksum":
				manifest.ContentsSHA256 = ""
			}
			if err := filecache.WriteJSON(filepath.Join(kit, "podplane-bundle.json"), manifest); err != nil {
				t.Fatal(err)
			}
			dest := deps.NewManager("http://127.0.0.1:1", filepath.Join(t.TempDir(), "deps"))
			if err := Import(context.Background(), dest, kit, "knc", "arm64", nil); err == nil {
				t.Fatal("invalid kit accepted")
			}
			if _, err := os.Stat(dest.ComponentsManifestCachePath()); !os.IsNotExist(err) {
				t.Fatal("failed validation published a manifest")
			}
		})
	}
}

// TestImportValidatesDependencyPaths rejects metadata that resolves artifacts
// outside the bundle even when those external files have the expected digest.
func TestImportValidatesDependencyPaths(t *testing.T) {
	for _, scenario := range []string{"vmconfig", "seed", "template", "oci-index", "gitdir", "commondir", "bare-git", "uncached-template", "cached-template"} {
		t.Run(scenario, func(t *testing.T) {
			directory, _ := testImportKit(t, "arm64")
			cache := deps.NewManager("", filepath.Join(directory, "deps"))
			body := []byte("external artifact")
			digest := fmt.Sprintf("%x", sha256.Sum256(body))
			external := t.TempDir()
			escape := strings.Repeat("../", 30) + strings.TrimPrefix(external, "/")
			if err := os.WriteFile(filepath.Join(external, "artifact"), body, 0644); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "vmconfig":
				manifest, _, err := cache.ReadCachedManifest("knc", "arm64")
				if err != nil {
					t.Fatal(err)
				}
				manifest.VMConfig.OS.Image = deps.Dependency{URL: "https://example.invalid/artifact", Version: escape, Digest: "sha256:" + digest, Size: int64(len(body))}
				if err := filecache.WriteJSON(cache.VMConfigManifestCachePath("knc", "arm64"), manifest); err != nil {
					t.Fatal(err)
				}
			case "seed":
				manifest := deps.SeedsManifest{Seeds: deps.Seeds{Version: escape, Snapshots: map[string]deps.SeedSnapshot{
					"example": {URL: "https://example.invalid/artifact", Digest: fmt.Sprintf("sha512:%x", sha512.Sum512(body)), Size: int64(len(body)), Cached: true},
				}}}
				if err := filecache.WriteJSON(cache.SeedsManifestCachePath(), manifest); err != nil {
					t.Fatal(err)
				}
			case "oci-index":
				repo := filepath.Join(cache.RegistryCacheDir(), "mirror/example.invalid/manifests/test")
				filename := filepath.Join(repo, "index.json")
				raw, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				var index v1.IndexManifest
				if err := json.Unmarshal(raw, &index); err != nil {
					t.Fatal(err)
				}
				tagged := &index.Manifests[1]
				if err := filecache.Copy(filepath.Join(repo, "blobs/sha256", tagged.Digest.Hex), filepath.Join(external, "tagged.json")); err != nil {
					t.Fatal(err)
				}
				tagged.Digest = v1.Hash{Algorithm: "sha256", Hex: escape + "/tagged.json"}
				if err := filecache.WriteJSON(filename, index); err != nil {
					t.Fatal(err)
				}
			case "gitdir", "commondir", "bare-git":
				// Plain files model a bare repository; no Git commands are needed.
				for name, content := range map[string]string{
					"config": "[core]\nrepositoryformatversion = 0\nbare = true\n",
					"HEAD":   "ref: refs/heads/main\n", "refs/heads/main": strings.Repeat("1", 40) + "\n",
				} {
					filename := filepath.Join(external, name)
					if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				source := &deps.ComponentsSource{URL: "https://example.invalid/components.git", Ref: deps.ComponentsSourceRef{Branch: "main"}}
				repo, err := cache.ComponentsGitCachePath(source.URL)
				if err != nil {
					t.Fatal(err)
				}
				if err := filecache.CopyTree(external, repo); err != nil {
					t.Fatal(err)
				}
				if scenario != "bare-git" {
					name, content := "commondir", external+"\n"
					if scenario == "gitdir" {
						name, content = ".git", "gitdir: "+external+"\n"
					}
					if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				manifest, err := cache.CachedComponentsManifest()
				if err != nil {
					t.Fatal(err)
				}
				manifest.Components.Source = source
				if err := filecache.WriteJSON(cache.ComponentsManifestCachePath(), manifest); err != nil {
					t.Fatal(err)
				}
			default:
				repo := filepath.Join(cache.TemplatesChartsCacheDir(), "podplane.local/templates/example")
				chart := deps.TemplateChart{Name: "example", Cached: scenario != "uncached-template", ChartLayerDigest: "sha256:" + digest}
				if scenario == "template" {
					repo = external
					chart.URL = "oci://" + escape
				}
				layer := filepath.Join(repo, "blobs/sha256", digest)
				if err := os.MkdirAll(filepath.Dir(layer), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(layer, body, 0644); err != nil {
					t.Fatal(err)
				}
				if err := filecache.WriteJSON(cache.TemplatesManifestCachePath(), deps.TemplatesManifest{Templates: deps.Templates{Charts: []deps.TemplateChart{chart}}}); err != nil {
					t.Fatal(err)
				}
			}
			// Rebuild a valid outer inventory so rejection must come from the
			// dependency metadata, not a stale checksum on its JSON file.
			filename := filepath.Join(directory, manifestFilename)
			raw, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			var manifest Manifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			files, err := contentsInventory(root)
			if err != nil {
				t.Fatal(err)
			}
			manifest.ContentsSHA256 = contentsChecksum(files)
			if err := filecache.WriteJSON(filename, manifest); err != nil {
				t.Fatal(err)
			}
			dest := deps.NewManager("", filepath.Join(t.TempDir(), "deps"))
			err = Import(context.Background(), dest, directory, "knc", "arm64", nil)
			if scenario == "cached-template" || scenario == "bare-git" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid dependency metadata accepted")
			}
			if _, err := os.Stat(dest.ComponentsManifestCachePath()); !os.IsNotExist(err) {
				t.Fatal("invalid dependency metadata changed the active cache")
			}
		})
	}
}

// imageCached checks exported OCI data through the standard layout reader,
// including tag or digest resolution and every referenced image blob.
func imageCached(directory string, image deps.ComponentImage) (bool, error) {
	tag, pinned, _ := strings.Cut(image.Image, "@")
	ref, err := name.ParseReference(tag)
	if err != nil {
		return false, err
	}
	repo := filepath.Join(directory, "mirror", filepath.FromSlash(ref.Context().Name()))
	if pinned != "" {
		digest, err := v1.NewHash(pinned)
		if err != nil {
			return false, err
		}
		if ok, err := filecache.Exists(filepath.Join(repo, "blobs", digest.Algorithm, digest.Hex), digest.Algorithm, digest.Hex); err != nil || !ok {
			return false, err
		}
	}
	lp, err := layout.FromPath(repo)
	if err != nil {
		return false, err
	}
	root, err := lp.ImageIndex()
	if err != nil {
		return false, err
	}
	index, err := root.IndexManifest()
	if err != nil {
		return false, err
	}
	for _, entry := range index.Manifests {
		if entry.Annotations["org.opencontainers.image.ref.name"] != ref.Identifier() && entry.Digest.String() != ref.Identifier() {
			continue
		}
		if entry.MediaType.IsIndex() {
			child, err := root.ImageIndex(entry.Digest)
			if err != nil {
				return false, err
			}
			index, err = child.IndexManifest()
			if err != nil {
				return false, err
			}
		} else {
			index = &v1.IndexManifest{Manifests: []v1.Descriptor{entry}}
		}
		for _, desc := range index.Manifests {
			if desc.Digest.String() != image.Digest {
				continue
			}
			img, err := lp.Image(desc.Digest)
			if err != nil {
				return false, err
			}
			manifest, err := img.Manifest()
			if err != nil {
				return false, err
			}
			for _, blob := range append(manifest.Layers, manifest.Config, desc) {
				if ok, err := filecache.Exists(filepath.Join(repo, "blobs", blob.Digest.Algorithm, blob.Digest.Hex), blob.Digest.Algorithm, blob.Digest.Hex); err != nil || !ok {
					return false, err
				}
			}
			return true, nil
		}
	}
	return false, nil
}
