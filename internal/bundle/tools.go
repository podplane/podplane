// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/filecache"
)

// release holds upstream GitHub release metadata, not a Podplane tool manifest.
type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

// asset identifies a GitHub release download and its optional upstream digest.
type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

// tools coordinates staged release metadata and persistent tool downloads.
type tools struct {
	stage    string
	cacheDir string
	client   *http.Client
	progress func(deps.DownloadEvent)
}

// install assembles upstream host tools and returns their versions.
// Intel macOS bundles omit QEMU, which recipients must preinstall.
func (b tools) install(ctx context.Context, directory, platform, version string) (map[string]string, error) {
	goos, arch, _ := strings.Cut(platform, "-")
	if goos == "macos" {
		goos = "darwin"
	}
	versions := map[string]string{}
	// Podplane uses its existing release archive, independently of the versions
	// of kubectl, Helm, mkcert and QEMU.
	cli, err := b.release(ctx, "podplane/podplane", version, "")
	if err != nil {
		return nil, err
	}
	cliName := fmt.Sprintf("podplane_%s_%s_%s.tar.gz", strings.TrimPrefix(cli.Tag, "v"), goos, arch)
	checksums, err := b.asset(ctx, cli, "podplane_"+strings.TrimPrefix(cli.Tag, "v")+"_checksums.txt")
	if err != nil {
		return nil, err
	}
	digest, err := checksum(checksums, cliName)
	if err != nil {
		return nil, err
	}
	cliAsset, err := cli.asset(cliName)
	if err != nil {
		return nil, err
	}
	archive, err := b.download(ctx, cliAsset.URL, "sha512:"+digest)
	if err != nil {
		return nil, err
	}
	if err := extractTools(archive, directory, toolArchivePath("podplane", "")); err != nil {
		return nil, err
	}
	versions["podplane"] = cli.Tag

	kubectl, err := b.metadata(ctx, "https://dl.k8s.io/release/stable.txt")
	if err != nil {
		return nil, err
	}
	versions["kubectl"] = strings.TrimSpace(string(kubectl))
	kubectlURL := fmt.Sprintf("https://dl.k8s.io/release/%s/bin/%s/%s/kubectl", versions["kubectl"], goos, arch)
	sum, err := b.metadata(ctx, kubectlURL+".sha256")
	if err != nil {
		return nil, err
	}
	if err := b.binary(ctx, kubectlURL, "sha256:"+strings.TrimSpace(string(sum)), directory, "kubectl"); err != nil {
		return nil, err
	}

	helm, err := b.release(ctx, "helm/helm", "", "")
	if err != nil {
		return nil, err
	}
	helmName := fmt.Sprintf("helm-%s-%s-%s.tar.gz", helm.Tag, goos, arch)
	sum, err = b.metadata(ctx, "https://get.helm.sh/"+helmName+".sha256sum")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(sum))
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty Helm checksum")
	}
	archive, err = b.download(ctx, "https://get.helm.sh/"+helmName, "sha256:"+fields[0])
	if err != nil {
		return nil, err
	}
	if err := extractTools(archive, directory, toolArchivePath("helm", goos+"-"+arch+"/")); err != nil {
		return nil, err
	}
	versions["helm"] = helm.Tag

	mkcert, err := b.release(ctx, "FiloSottile/mkcert", "", "")
	if err != nil {
		return nil, err
	}
	asset, err := mkcert.asset(fmt.Sprintf("mkcert-%s-%s-%s", mkcert.Tag, goos, arch))
	if err != nil {
		return nil, err
	}
	if err := b.binary(ctx, asset.URL, asset.Digest, directory, "mkcert"); err != nil {
		return nil, err
	}
	versions["mkcert"] = mkcert.Tag

	executables := []string{"podplane", "kubectl", "helm", "mkcert"}
	if platform != "macos-amd64" {
		target := "aarch64"
		if arch == "amd64" {
			target = "x86_64"
		}
		versions["qemu"], err = b.installQEMU(ctx, directory, goos, arch, target)
		if err != nil {
			return nil, err
		}
		executables = append(executables, "qemu-img", "qemu-system-"+target)
	}
	for _, name := range executables {
		info, err := os.Stat(filepath.Join(directory, "bin", name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return nil, fmt.Errorf("missing executable bin/%s for %s", name, platform)
		}
	}
	return versions, nil
}

// installQEMU installs portable binaries, libraries and firmware from the
// platform's upstream distributor and returns its release tag.
func (b tools) installQEMU(ctx context.Context, directory, goos, arch, target string) (string, error) {
	repository, prefix := "hermeticbuild/qemu-prebuilt", ""
	if goos == "darwin" {
		repository, prefix = "jzombie/lab-qemu-portable", "qemu-v"
	}
	qemu, err := b.release(ctx, repository, "", prefix)
	if err != nil {
		return "", err
	}
	names := []string{
		fmt.Sprintf("qemu-img-linux-%s-%s.tar.gz", arch, qemu.Tag),
		fmt.Sprintf("qemu-system-bin-linux-%s-%s-softmmu-%s.tar.gz", arch, target, qemu.Tag),
		fmt.Sprintf("qemu-system-data-linux-%s-%s.tar.gz", arch, qemu.Tag),
	}
	if goos == "darwin" {
		// Rebuild tags include a packaging suffix that is not in the asset name.
		names = nil
		for _, asset := range qemu.Assets {
			if strings.HasPrefix(asset.Name, "qemu-portable-macos-arm64-") && strings.HasSuffix(asset.Name, ".tar.gz") {
				names = append(names, asset.Name)
			}
		}
		if len(names) != 1 {
			return "", fmt.Errorf("release %s must provide one portable macOS arm64 QEMU archive", qemu.Tag)
		}
	}
	for _, name := range names {
		archive, err := b.asset(ctx, qemu, name)
		if err != nil {
			return "", err
		}
		if err := extractTools(archive, directory, func(name string) string {
			name = strings.TrimPrefix(name, "qemu-portable/")
			if strings.HasPrefix(name, "bin/") || strings.HasPrefix(name, "lib/") || strings.HasPrefix(name, "share/") {
				return name
			}
			return ""
		}); err != nil {
			return "", err
		}
	}
	return qemu.Tag, nil
}

// asset finds a named release asset with a download URL.
func (r release) asset(name string) (asset, error) {
	for _, asset := range r.Assets {
		if asset.Name == name && asset.URL != "" {
			return asset, nil
		}
	}
	return asset{}, fmt.Errorf("upstream release %s has no asset %s", r.Tag, name)
}

// asset caches a named release asset, checking its upstream digest when available.
func (b tools) asset(ctx context.Context, release release, name string) (string, error) {
	asset, err := release.asset(name)
	if err != nil {
		return "", err
	}
	return b.download(ctx, asset.URL, asset.Digest)
}

// release resolves an explicit tag or the latest stable GitHub release,
// optionally restricting selection to tags with a given prefix.
func (b tools) release(ctx context.Context, repo, version, prefix string) (release, error) {
	endpoint := "https://api.github.com/repos/" + repo + "/releases"
	switch {
	case version != "":
		endpoint += "/tags/" + version
	case prefix == "":
		endpoint += "/latest"
	default:
		endpoint += "?per_page=100"
	}
	raw, err := b.metadata(ctx, endpoint)
	if err != nil {
		return release{}, err
	}
	var releases []release
	if prefix == "" {
		var release release
		if err := json.Unmarshal(raw, &release); err != nil {
			return release, err
		}
		releases = append(releases, release)
	} else if err := json.Unmarshal(raw, &releases); err != nil {
		return release{}, err
	}
	for _, release := range releases {
		if release.Tag != "" && !release.Draft && (version != "" || !release.Prerelease) && strings.HasPrefix(release.Tag, prefix) {
			return release, nil
		}
	}
	return release{}, fmt.Errorf("no suitable published release of %s", repo)
}

// metadata freezes upstream responses for this preparation, so retries and all
// platforms use the same versions. Removing staging allows fresh resolution.
func (b tools) metadata(ctx context.Context, url string) ([]byte, error) {
	filename := filepath.Join(b.stage, "upstream", fmt.Sprintf("%x", sha256.Sum256([]byte(url))))
	if raw, err := os.ReadFile(filename); err == nil {
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := filecache.Download(ctx, url, filename, "sha256", "", filecache.DownloadOptions{Client: b.client, Progress: func(int64, int64) {}}); err != nil {
		return nil, err
	}
	return os.ReadFile(filename)
}

// download keeps artifacts independently of staging. For older upstream assets
// without a published digest, the first download establishes a local checksum;
// that detects later cache damage, but is not upstream authentication.
func (b tools) download(ctx context.Context, url, digest string) (_ string, err error) {
	filename := filepath.Join(b.cacheDir, "tools", fmt.Sprintf("%x", sha256.Sum256([]byte(url))), path.Base(url))
	state := deps.DownloadEventDone
	if b.progress != nil {
		b.progress(deps.DownloadEvent{Type: deps.DownloadEventChecking, Name: path.Base(url)})
		defer func() {
			event := deps.DownloadEvent{Type: state, Name: path.Base(url), Err: err}
			if err != nil {
				event.Type = deps.DownloadEventFailed
			} else if info, statErr := os.Stat(filename); statErr == nil {
				event.Current, event.Total = info.Size(), info.Size()
			}
			b.progress(event)
		}()
	}
	algo, sum := "sha256", ""
	if digest != "" {
		var ok bool
		algo, sum, ok = strings.Cut(digest, ":")
		if !ok || sum == "" {
			return "", fmt.Errorf("invalid upstream digest %q", digest)
		}
	} else if raw, err := os.ReadFile(filename + ".sha256"); err == nil {
		sum = strings.TrimSpace(string(raw))
	}
	if cached, _ := filecache.Exists(filename, algo, sum); cached {
		state = deps.DownloadEventCached
		return filename, nil
	}
	// A mismatching local checksum means cache damage, not a reason to trust a
	// different remote artifact. Verify the replacement against the same digest.
	if _, err := filecache.Download(ctx, url, filename, algo, sum, filecache.DownloadOptions{
		Client: b.client,
		Progress: func(current, total int64) {
			if b.progress != nil {
				b.progress(deps.DownloadEvent{Type: deps.DownloadEventProgress, Name: path.Base(url), Current: current, Total: total})
			}
		},
	}); err != nil {
		return "", err
	}
	if digest == "" && sum == "" {
		f, err := os.Open(filename)
		if err != nil {
			return "", err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filename+".sha256", []byte(fmt.Sprintf("%x\n", h.Sum(nil))), 0644); err != nil {
			return "", err
		}
	}
	return filename, nil
}

// binary copies a cached tool download into the bundle's bin directory and makes it executable.
func (b tools) binary(ctx context.Context, url, digest, directory, name string) error {
	filename, err := b.download(ctx, url, digest)
	if err != nil {
		return err
	}
	dest := filepath.Join(directory, "bin", name)
	if err := filecache.Copy(filename, dest); err != nil {
		return err
	}
	return os.Chmod(dest, 0755)
}

// checksum looks up an asset's digest in an upstream checksum file.
func checksum(filename, asset string) (string, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("upstream checksums do not include %s", asset)
}

// toolArchivePath maps a tool executable and its license to bundle paths,
// excluding other archive entries.
func toolArchivePath(tool, prefix string) func(string) string {
	return func(name string) string {
		if name == prefix+tool {
			return "bin/" + tool
		}
		if name == prefix+"LICENSE" {
			return "share/licenses/" + tool + "/LICENSE"
		}
		return ""
	}
}

// extractTools extracts selected regular files from a gzip-compressed tar
// archive, rejecting unsafe paths, duplicate destinations and selected links.
func extractTools(archive, directory string, rename func(string) string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		original := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
		if (original == "." || original == "") && header.Typeflag == tar.TypeDir {
			continue
		}
		if !filecache.ValidPath(original) {
			return fmt.Errorf("invalid runtime archive path %q", header.Name)
		}
		name := rename(original)
		if name == "" || header.Typeflag == tar.TypeDir {
			continue
		}
		if !filecache.ValidPath(name) || seen[name] {
			return fmt.Errorf("invalid or duplicate runtime archive path %q", header.Name)
		}
		seen[name] = true
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("runtime archive must contain regular files: %s", name)
		}
		mode := os.FileMode(0644)
		if header.Mode&0111 != 0 || path.Dir(name) == "bin" {
			mode = 0755
		}
		if err := filecache.WriteFile(root, name, mode, func(w io.Writer) error {
			_, err := io.Copy(w, tr)
			return err
		}); err != nil {
			return err
		}
	}
}
