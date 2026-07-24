// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/podplane/podplane/internal/filecache"
)

const defaultDownloadConcurrency = 4

var registryConcurrencyOverrides = map[string]int{
	"public.ecr.aws": 1,
}

// DownloadEventType describes a dependency download progress event.
type DownloadEventType string

const (
	DownloadEventStatus   DownloadEventType = "status"
	DownloadEventChecking DownloadEventType = "checking"
	DownloadEventCached   DownloadEventType = "cached"
	DownloadEventQueued   DownloadEventType = "queued"
	DownloadEventStarted  DownloadEventType = "started"
	DownloadEventProgress DownloadEventType = "progress"
	DownloadEventDone     DownloadEventType = "done"
	DownloadEventFailed   DownloadEventType = "failed"
)

// DownloadEvent reports progress for a single dependency artifact, or a
// top-level status message when Name is empty.
type DownloadEvent struct {
	Type    DownloadEventType
	Name    string
	Path    string
	Message string
	Current int64
	Total   int64
	Err     error
}

// DownloadOptions controls dependency download behavior.
type DownloadOptions struct {
	DryRun      bool
	Concurrency int
	Client      *http.Client
	Progress    func(DownloadEvent)
	Output      io.Writer
	Archs       []string
	Providers   []string
	Addons      []string

	// SkipCrossArchDependencies skips dependency cache population shared across
	// VMConfig architectures, such as component images, template charts, and seed
	// snapshots. It is used by callers downloading multiple archs after the first
	// arch has already populated those shared caches.
	SkipCrossArchDependencies bool

	// SkipSeeds skips loading and caching seed manifests and snapshots.
	SkipSeeds bool

	// VMConfigManifestPath, when set, reads the vmconfig manifest from this
	// local JSON file instead of fetching it from the configured deps base URL.
	VMConfigManifestPath string

	// ComponentsManifestPath, when set, reads the components manifest from this
	// local JSON file instead of fetching it from the configured deps base URL.
	ComponentsManifestPath string

	// SkipComponentsGit skips fetching the Git source declared by the components
	// manifest.
	SkipComponentsGit bool

	// TemplatesManifestPath, when set, reads the templates manifest from this
	// local JSON file instead of fetching it from the configured deps base URL.
	TemplatesManifestPath string

	// SeedsManifestPath, when set, reads the seeds manifest from this local JSON
	// file instead of fetching it from the configured deps base URL.
	SeedsManifestPath string
}

type pendingDownload struct {
	Item           Item
	Dest           string
	Algo           string
	Checksum       string
	Image          ComponentImage
	VMConfigIndex  int
	ComponentIndex int
	TemplateIndex  int
	Kind           pendingDownloadKind
}

type pendingDownloadKind string

const (
	pendingDownloadVMConfig       pendingDownloadKind = "vmconfig"
	pendingDownloadVMConfigImage  pendingDownloadKind = "vmconfig-image"
	pendingDownloadComponentImage pendingDownloadKind = "component-image"
	pendingDownloadTemplateImage  pendingDownloadKind = "template-image"
)

// Download fetches the latest manifests and downloads any referenced vmconfig
// artifacts and component images into the local deps cache. The manifests are
// cached on success so they can be reused later.
//
// If dryRun is true, no network downloads are performed and the manifest is
// not written to the cache; instead, a summary of what would be downloaded
// is printed. Items that are already cached (with matching sha256) are
// skipped in both modes — dry-run only lists files that would actually be
// downloaded.
func (m *Manager) Download(kind, arch string, opts DownloadOptions) error {
	output := opts.Output
	if output == nil {
		output = os.Stdout
	}

	progress := opts.Progress
	var progressMu sync.Mutex
	emit := func(event DownloadEvent) {
		if progress == nil {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		progress(event)
	}

	client := opts.Client
	if client == nil {
		client = newDownloadHTTPClient(opts.Concurrency)
	}

	ctx := context.Background()
	vmconfigManifestSource := fmt.Sprintf("%s/manifests/%s", m.baseURL, manifestFilename(kind, arch))
	var manifest *Manifest
	var err error
	if opts.VMConfigManifestPath != "" {
		vmconfigManifestSource = opts.VMConfigManifestPath
		emit(DownloadEvent{Type: DownloadEventStatus, Message: fmt.Sprintf("Reading vmconfig manifest from %s...", opts.VMConfigManifestPath)})
		manifest, err = readVMConfigManifestFile(opts.VMConfigManifestPath)
	} else {
		emit(DownloadEvent{Type: DownloadEventStatus, Message: "Fetching vmconfig manifest..."})
		manifest, err = m.fetchManifest(ctx, kind, arch, client)
	}
	if err != nil {
		return fmt.Errorf("failed to load vmconfig manifest: %w", err)
	}
	if opts.VMConfigManifestPath == "" {
		for _, it := range manifest.Items() {
			if it.IsUnreleasedVMConfigStub() {
				return fmt.Errorf("published vmconfig manifest contains unreleased vmconfig stub")
			}
		}
	}
	manifest.ResetCached()
	items := manifest.DownloadItems(ItemFilter{Providers: opts.Providers})
	vmconfigImages := manifest.VMConfig.Images
	for _, it := range items {
		if _, _, err := it.Dep.ParseDigest(); err != nil {
			return fmt.Errorf("invalid digest for %s: %w", it.Name, err)
		}
		if it.Dep.URL == "" {
			return fmt.Errorf("missing URL for %s", it.Name)
		}
		if it.Dep.Size <= 0 {
			return fmt.Errorf("missing size for %s", it.Name)
		}
	}
	for i, image := range vmconfigImages {
		if image.Image == "" {
			return fmt.Errorf("vmconfig image entry %d has empty image", i)
		}
		if image.Digest == "" {
			return fmt.Errorf("vmconfig image %s has empty digest", image.Image)
		}
		if image.Size <= 0 {
			return fmt.Errorf("vmconfig image %s has missing size", image.Image)
		}
	}

	componentsManifest := &ComponentsManifest{}
	componentsSource := ""
	componentIndexes := []int{}
	if !opts.SkipCrossArchDependencies {
		componentsManifest, componentsSource, err = m.loadComponentsManifest(ctx, client, opts, emit)
		if err != nil {
			return err
		}
		if !opts.DryRun && !opts.SkipComponentsGit {
			emit(DownloadEvent{Type: DownloadEventStatus, Message: "Caching components Git source..."})
			if err := m.EnsureComponentsGitCached(ctx, componentsManifest.Components.Source); err != nil {
				return fmt.Errorf("failed to cache components Git source: %w", err)
			}
		}
		componentsManifest.ResetCached()
		componentArchs, err := normalizeArchs(opts.Archs, arch)
		if err != nil {
			return err
		}
		componentIndexes = componentsManifest.DownloadImageIndexes(ComponentImageFilter{Archs: componentArchs, Providers: opts.Providers, Addons: opts.Addons})
	}
	templatesManifest := &TemplatesManifest{}
	templatesSource := ""
	templateImageIndexes := []int{}
	if !opts.SkipCrossArchDependencies {
		templatesManifest, templatesSource, err = m.loadTemplatesManifest(ctx, client, opts, emit)
		if err != nil {
			return err
		}
		templatesManifest.ResetCached()
		templateArchs, err := normalizeArchs(opts.Archs, arch)
		if err != nil {
			return err
		}
		templateImageIndexes = templatesManifest.DownloadImageIndexes(templateArchs)
	}
	seedsManifest := &SeedsManifest{}
	seedsSource := ""
	if !opts.SkipCrossArchDependencies && !opts.SkipSeeds {
		seedsManifest, seedsSource, err = m.loadSeedsManifest(ctx, client, opts, emit)
		if err != nil {
			return err
		}
		seedsManifest.ResetCached()
	}
	for _, index := range componentIndexes {
		image := componentsManifest.Components.Images[index]
		if image.Image == "" {
			return fmt.Errorf("component %s has empty image", image.componentLabel())
		}
		if image.Digest == "" {
			return fmt.Errorf("component %s image %s has empty digest", image.componentLabel(), image.Image)
		}
		if image.Size <= 0 {
			return fmt.Errorf("component %s image %s has missing size", image.componentLabel(), image.Image)
		}
	}
	for _, index := range templateImageIndexes {
		image := templatesManifest.Templates.Images[index]
		if image.Image == "" {
			return fmt.Errorf("template image entry %d has empty image", index)
		}
		if image.Digest == "" {
			return fmt.Errorf("template image %s has empty digest", image.Image)
		}
		if image.Size <= 0 {
			return fmt.Errorf("template image %s has missing size", image.Image)
		}
	}

	if opts.DryRun {
		_, _ = fmt.Fprintf(output, "VMConfig manifest: %s\n", vmconfigManifestSource)
		_, _ = fmt.Fprintf(output, "VMConfig cache directory: %s\n", m.VMConfigCacheDir())
		_, _ = fmt.Fprintln(output, "VMConfig artifacts that would be downloaded:")
	} else {
		totalItems := len(items) + len(vmconfigImages) + len(componentIndexes) + len(templateImageIndexes)
		emit(DownloadEvent{Type: DownloadEventStatus, Message: fmt.Sprintf("Checking %d vmconfig artifacts and image mirror entries in cache...", totalItems)})
		for _, it := range items {
			emit(DownloadEvent{Type: DownloadEventChecking, Name: it.Name, Total: it.Dep.Size})
		}
		for _, image := range vmconfigImages {
			emit(DownloadEvent{Type: DownloadEventChecking, Name: image.Image, Total: image.Size})
		}
		for _, index := range componentIndexes {
			image := componentsManifest.Components.Images[index]
			emit(DownloadEvent{Type: DownloadEventChecking, Name: image.Image, Total: image.Size})
		}
		for _, index := range templateImageIndexes {
			image := templatesManifest.Templates.Images[index]
			emit(DownloadEvent{Type: DownloadEventChecking, Name: image.Image, Total: image.Size})
		}
	}

	pending := make([]pendingDownload, 0, len(items)+len(vmconfigImages)+len(componentIndexes)+len(templateImageIndexes))
	missing := 0
	imageMissing := 0
	vmconfigPending := 0
	imagePending := 0
	for _, it := range items {
		algo, hex, err := it.Dep.ParseDigest()
		if err != nil {
			return fmt.Errorf("invalid digest for %s: %w", it.Name, err)
		}
		if it.Dep.URL == "" {
			return fmt.Errorf("missing URL for %s", it.Name)
		}
		if it.Dep.Size <= 0 {
			return fmt.Errorf("missing size for %s", it.Name)
		}

		dest := m.VMConfigArtifactCachePath(it.Name, it.Dep)
		exists, err := filecache.Exists(dest, algo, hex)
		if err != nil {
			return fmt.Errorf("failed to check cache for %s: %w", it.Name, err)
		}
		if exists {
			manifest.MarkCached(it.Name)
			emit(DownloadEvent{Type: DownloadEventCached, Name: it.Name, Path: dest, Current: it.Dep.Size, Total: it.Dep.Size})
			continue
		}
		missing++

		if opts.DryRun {
			rel := strings.TrimPrefix(
				strings.TrimPrefix(dest, m.depsCacheDir),
				string(filepath.Separator),
			)
			_, _ = fmt.Fprintf(output, "  • %s\n", rel)
			continue
		}

		pending = append(pending, pendingDownload{Kind: pendingDownloadVMConfig, Item: it, Dest: dest, Algo: algo, Checksum: hex})
		vmconfigPending++
		emit(DownloadEvent{Type: DownloadEventQueued, Name: it.Name, Path: dest})
	}
	for index, vmconfigImage := range vmconfigImages {
		image := vmconfigComponentImage(vmconfigImage)
		cached, err := componentImageCached(m.RegistryCacheDir(), image)
		if err != nil {
			return err
		}
		if cached {
			manifest.MarkImageCached(index)
			emit(DownloadEvent{Type: DownloadEventCached, Name: image.Image, Current: image.Size, Total: image.Size})
			continue
		}
		imageMissing++
		if !opts.DryRun {
			pending = append(pending, pendingDownload{Kind: pendingDownloadVMConfigImage, Image: image, VMConfigIndex: index})
			imagePending++
			emit(DownloadEvent{Type: DownloadEventQueued, Name: image.Image, Total: image.Size})
		}
	}
	for _, index := range componentIndexes {
		image := componentsManifest.Components.Images[index]
		cached, err := componentImageCached(m.RegistryCacheDir(), image)
		if err != nil {
			return err
		}
		if cached {
			componentsManifest.MarkCached(index)
			emit(DownloadEvent{Type: DownloadEventCached, Name: image.Image, Current: image.Size, Total: image.Size})
			continue
		}
		imageMissing++
		if !opts.DryRun {
			pending = append(pending, pendingDownload{Kind: pendingDownloadComponentImage, Image: image, ComponentIndex: index})
			imagePending++
			emit(DownloadEvent{Type: DownloadEventQueued, Name: image.Image, Total: image.Size})
		}
	}
	for _, index := range templateImageIndexes {
		templateImage := templatesManifest.Templates.Images[index]
		image := templateComponentImage(templateImage)
		cached, err := componentImageCached(m.RegistryCacheDir(), image)
		if err != nil {
			return err
		}
		if cached {
			templatesManifest.MarkImageCached(index)
			emit(DownloadEvent{Type: DownloadEventCached, Name: image.Image, Current: image.Size, Total: image.Size})
			continue
		}
		imageMissing++
		if !opts.DryRun {
			pending = append(pending, pendingDownload{Kind: pendingDownloadTemplateImage, Image: image, TemplateIndex: index})
			imagePending++
			emit(DownloadEvent{Type: DownloadEventQueued, Name: image.Image, Total: image.Size})
		}
	}

	if opts.DryRun {
		if missing == 0 && imageMissing == 0 {
			_, _ = fmt.Fprintln(output, "All dependencies already cached, nothing to download.")
		}
		if !opts.SkipCrossArchDependencies {
			_, _ = fmt.Fprintf(output, "Components manifest: %s\n", componentsSource)
			_, _ = fmt.Fprintf(output, "Registry cache directory: %s\n", m.RegistryCacheDir())
			_, _ = fmt.Fprintf(output, "VMConfig images: %d\n", len(vmconfigImages))
			_, _ = fmt.Fprintf(output, "Component images: %d\n", len(componentIndexes))
		}
		if !opts.SkipCrossArchDependencies {
			_, _ = fmt.Fprintf(output, "Templates manifest: %s\n", templatesSource)
			_, _ = fmt.Fprintf(output, "Template chart cache directory: %s\n", m.TemplatesChartsCacheDir())
			_, _ = fmt.Fprintf(output, "Template charts: %d\n", len(templatesManifest.Templates.Charts))
			_, _ = fmt.Fprintf(output, "Template images: %d\n", len(templateImageIndexes))
		}
		if !opts.SkipCrossArchDependencies && !opts.SkipSeeds {
			_, _ = fmt.Fprintf(output, "Seeds manifest: %s\n", seedsSource)
			_, _ = fmt.Fprintf(output, "Seed snapshot cache directory: %s\n", m.SeedsSnapshotsCacheDir())
			_, _ = fmt.Fprintf(output, "Seed snapshots: %d\n", len(seedsManifest.Seeds.Snapshots))
		}
		return nil
	}

	if len(pending) == 0 {
		emit(DownloadEvent{Type: DownloadEventStatus, Message: "All dependencies already cached, nothing to download."})
	} else {
		emit(DownloadEvent{Type: DownloadEventStatus, Message: downloadPendingMessage(vmconfigPending, imagePending)})
		if err := m.downloadPending(pending, opts.Concurrency, client, emit); err != nil {
			return err
		}
		for _, job := range pending {
			switch job.Kind {
			case pendingDownloadVMConfigImage:
				manifest.MarkImageCached(job.VMConfigIndex)
			case pendingDownloadComponentImage:
				componentsManifest.MarkCached(job.ComponentIndex)
			case pendingDownloadTemplateImage:
				templatesManifest.MarkImageCached(job.TemplateIndex)
			default:
				manifest.MarkCached(job.Item.Name)
			}
		}
	}
	filteredRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode cached vmconfig manifest: %w", err)
	}
	raw := append(filteredRaw, '\n')

	if err := m.WriteCachedManifest(kind, arch, raw); err != nil {
		return fmt.Errorf("failed to write cached manifest: %w", err)
	}

	if !opts.SkipCrossArchDependencies {
		filteredRaw, err := json.MarshalIndent(componentsManifest, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to encode cached components manifest: %w", err)
		}
		componentsRaw := append(filteredRaw, '\n')
		if err := m.WriteCachedComponentsManifest(componentsRaw); err != nil {
			return fmt.Errorf("failed to write cached components manifest: %w", err)
		}
	}
	if !opts.SkipCrossArchDependencies {
		if err := m.populateTemplatesCache(ctx, templatesManifest, opts.TemplatesManifestPath, emit); err != nil {
			return fmt.Errorf("failed to cache template charts: %w", err)
		}
		filteredRaw, err = json.MarshalIndent(templatesManifest, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to encode cached templates manifest: %w", err)
		}
		templatesRaw := append(filteredRaw, '\n')
		if err := m.WriteCachedTemplatesManifest(templatesRaw); err != nil {
			return fmt.Errorf("failed to write cached templates manifest: %w", err)
		}
	}
	if !opts.SkipCrossArchDependencies && !opts.SkipSeeds {
		if err := m.populateSeedsCache(ctx, seedsManifest, opts.SeedsManifestPath, client, emit); err != nil {
			return fmt.Errorf("failed to cache seed snapshots: %w", err)
		}
		filteredRaw, err = json.MarshalIndent(seedsManifest, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to encode cached seeds manifest: %w", err)
		}
		seedsRaw := append(filteredRaw, '\n')
		if err := m.WriteCachedSeedsManifest(seedsRaw); err != nil {
			return fmt.Errorf("failed to write cached seeds manifest: %w", err)
		}
	}
	emit(DownloadEvent{Type: DownloadEventStatus, Message: fmt.Sprintf("All dependencies for %s_%s_%s are up to date.", kind, OS, arch)})
	return nil
}

// loadComponentsManifest loads the components manifest from either a local path or the configured deps base URL.
func (m *Manager) loadComponentsManifest(ctx context.Context, client *http.Client, opts DownloadOptions, emit func(DownloadEvent)) (*ComponentsManifest, string, error) {
	componentsSource := fmt.Sprintf("%s/%s", m.baseURL, componentsManifestPath)
	var componentsManifest *ComponentsManifest
	var err error
	if opts.ComponentsManifestPath != "" {
		componentsSource = opts.ComponentsManifestPath
		emit(DownloadEvent{Type: DownloadEventStatus, Message: fmt.Sprintf("Reading components manifest from %s...", opts.ComponentsManifestPath)})
		componentsManifest, err = readComponentsManifestFile(opts.ComponentsManifestPath)
	} else {
		emit(DownloadEvent{Type: DownloadEventStatus, Message: "Fetching components manifest..."})
		componentsManifest, err = m.fetchComponentsManifest(ctx, client)
	}
	if err != nil {
		return nil, componentsSource, fmt.Errorf("failed to load components manifest: %w", err)
	}
	return componentsManifest, componentsSource, nil
}

// loadTemplatesManifest loads the templates manifest from either a local path or the configured deps base URL.
func (m *Manager) loadTemplatesManifest(ctx context.Context, client *http.Client, opts DownloadOptions, emit func(DownloadEvent)) (*TemplatesManifest, string, error) {
	templatesSource := fmt.Sprintf("%s/%s", m.baseURL, templatesManifestPath)
	var templatesManifest *TemplatesManifest
	var err error
	if opts.TemplatesManifestPath != "" {
		templatesSource = opts.TemplatesManifestPath
		emit(DownloadEvent{Type: DownloadEventStatus, Message: fmt.Sprintf("Reading templates manifest from %s...", opts.TemplatesManifestPath)})
		templatesManifest, err = readTemplatesManifestFile(opts.TemplatesManifestPath)
	} else {
		emit(DownloadEvent{Type: DownloadEventStatus, Message: "Fetching templates manifest..."})
		templatesManifest, err = m.fetchTemplatesManifest(ctx, client)
	}
	if err != nil {
		return nil, templatesSource, fmt.Errorf("failed to load templates manifest: %w", err)
	}
	return templatesManifest, templatesSource, nil
}

// loadSeedsManifest loads the seeds manifest from either a local path or the configured deps base URL.
func (m *Manager) loadSeedsManifest(ctx context.Context, client *http.Client, opts DownloadOptions, emit func(DownloadEvent)) (*SeedsManifest, string, error) {
	seedsSource := fmt.Sprintf("%s/%s", m.baseURL, seedsManifestPath)
	var seedsManifest *SeedsManifest
	var err error
	if opts.SeedsManifestPath != "" {
		seedsSource = opts.SeedsManifestPath
		emit(DownloadEvent{Type: DownloadEventStatus, Message: fmt.Sprintf("Reading seeds manifest from %s...", opts.SeedsManifestPath)})
		seedsManifest, err = readSeedsManifestFile(opts.SeedsManifestPath)
	} else {
		emit(DownloadEvent{Type: DownloadEventStatus, Message: "Fetching seeds manifest..."})
		seedsManifest, err = m.fetchSeedsManifest(ctx, client)
	}
	if err != nil {
		return nil, seedsSource, fmt.Errorf("failed to load seeds manifest: %w", err)
	}
	return seedsManifest, seedsSource, nil
}

func downloadPendingMessage(vmconfigPending, imagePending int) string {
	if vmconfigPending == 0 {
		return fmt.Sprintf("Populating image mirror store with %d images...", imagePending)
	}
	if imagePending == 0 {
		return fmt.Sprintf("Downloading %d vmconfig artifacts...", vmconfigPending)
	}
	return fmt.Sprintf("Downloading %d vmconfig artifacts and populating image mirror store with %d images...", vmconfigPending, imagePending)
}

func (m *Manager) downloadPending(pending []pendingDownload, concurrency int, client *http.Client, emit func(DownloadEvent)) error {
	if concurrency <= 0 {
		concurrency = defaultDownloadConcurrency
	}
	if concurrency > len(pending) {
		concurrency = len(pending)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	var repoLocksMu sync.Mutex
	repoLocks := map[string]*sync.Mutex{}
	globalSem := make(chan struct{}, concurrency)
	hostSemaphores := registryConcurrencySemaphores(registryConcurrencyOverrides)

	setErr := func(err error) bool {
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr != nil {
			return false
		}
		firstErr = err
		cancel()
		return true
	}
	componentRepoLock := func(image ComponentImage) *sync.Mutex {
		repo := mirrorRepoFromChartImage(defaultMirrorPrefix, image.Image)
		repoLocksMu.Lock()
		defer repoLocksMu.Unlock()
		lock := repoLocks[repo]
		if lock == nil {
			lock = &sync.Mutex{}
			repoLocks[repo] = lock
		}
		return lock
	}
	acquire := func(sem chan struct{}) bool {
		select {
		case sem <- struct{}{}:
			return true
		case <-ctx.Done():
			return false
		}
	}
	release := func(sem chan struct{}) {
		<-sem
	}
	hostSemaphore := func(job pendingDownload) chan struct{} {
		if job.Kind != pendingDownloadVMConfigImage && job.Kind != pendingDownloadComponentImage && job.Kind != pendingDownloadTemplateImage {
			return nil
		}
		return hostSemaphores[registryHostFromImage(job.Image.Image)]
	}

	for _, job := range pending {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(job pendingDownload) {
			defer wg.Done()

			hostSem := hostSemaphore(job)
			if hostSem != nil {
				if !acquire(hostSem) {
					return
				}
				defer release(hostSem)
			}
			if !acquire(globalSem) {
				return
			}
			defer release(globalSem)

			switch job.Kind {
			case pendingDownloadVMConfigImage, pendingDownloadComponentImage, pendingDownloadTemplateImage:
				lock := componentRepoLock(job.Image)
				lock.Lock()
				defer lock.Unlock()

				emit(DownloadEvent{Type: DownloadEventStarted, Name: job.Image.Image, Total: job.Image.Size})
				imageProgress := &componentImageProgress{name: job.Image.Image, total: job.Image.Size, emit: emit}
				imageProgress.report()
				err := writeComponentImage(ctx, m.RegistryCacheDir(), job.Image, imageProgress)
				if err != nil {
					wrapped := fmt.Errorf("failed to download %s: %w", job.Image.Image, err)
					if setErr(wrapped) {
						emit(DownloadEvent{Type: DownloadEventFailed, Name: job.Image.Image, Err: err})
					}
					return
				}
				emit(DownloadEvent{Type: DownloadEventDone, Name: job.Image.Image, Current: imageProgress.current, Total: imageProgress.total})
			default:
				emit(DownloadEvent{Type: DownloadEventStarted, Name: job.Item.Name, Path: job.Dest})
				_, err := filecache.Download(ctx, job.Item.Dep.URL, job.Dest, job.Algo, job.Checksum, filecache.DownloadOptions{
					Client: client,
					Total:  job.Item.Dep.Size,
					Progress: func(current, total int64) {
						emit(DownloadEvent{Type: DownloadEventProgress, Name: job.Item.Name, Path: job.Dest, Current: current, Total: total})
					},
				})
				if err != nil {
					wrapped := fmt.Errorf("failed to download %s: %w", job.Item.Name, err)
					if setErr(wrapped) {
						emit(DownloadEvent{Type: DownloadEventFailed, Name: job.Item.Name, Path: job.Dest, Err: err})
					}
					return
				}
				emit(DownloadEvent{Type: DownloadEventDone, Name: job.Item.Name, Path: job.Dest, Current: job.Item.Dep.Size, Total: job.Item.Dep.Size})
			}
		}(job)
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	return nil
}

func registryHostFromImage(image string) string {
	repo, _, _ := splitImageRef(image)
	first := repo
	if slash := strings.Index(repo, "/"); slash >= 0 {
		first = repo[:slash]
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return "docker.io"
}

func registryConcurrencySemaphores(overrides map[string]int) map[string]chan struct{} {
	semaphores := make(map[string]chan struct{}, len(overrides))
	for registry, limit := range overrides {
		if limit <= 0 {
			continue
		}
		semaphores[registry] = make(chan struct{}, limit)
	}
	return semaphores
}

// normalizeArchs returns a de-duplicated list of supported target architectures.
func normalizeArchs(values []string, defaultArch string) ([]string, error) {
	if len(values) == 0 {
		values = []string{defaultArch}
	}
	seen := map[string]bool{}
	archs := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			arch := strings.TrimSpace(part)
			if arch == "" {
				continue
			}
			switch arch {
			case "amd64", "arm64":
			default:
				return nil, fmt.Errorf("unsupported arch %q", arch)
			}
			if !seen[arch] {
				seen[arch] = true
				archs = append(archs, arch)
			}
		}
	}
	if len(archs) == 0 {
		return nil, fmt.Errorf("at least one arch is required")
	}
	return archs, nil
}
