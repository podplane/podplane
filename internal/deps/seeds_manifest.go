// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deps

// SeedsManifest is the top-level seed snapshot dependency manifest.
type SeedsManifest struct {
	Seeds Seeds `json:"seeds"`
}

// Seeds groups named snapshots with their release and component versions.
type Seeds struct {
	Version    string                  `json:"version"`
	Components string                  `json:"components"`
	Snapshots  map[string]SeedSnapshot `json:"snapshots"`
}

// SeedSnapshot describes a local or remote snapshot and its integrity and cache metadata.
type SeedSnapshot struct {
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Digest string `json:"digest,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Cached bool   `json:"cached,omitempty"`
}

// ResetCached clears local cache-state markers from seed snapshots.
func (m *SeedsManifest) ResetCached() {
	if m == nil {
		return
	}
	for name, snapshot := range m.Seeds.Snapshots {
		snapshot.Cached = false
		m.Seeds.Snapshots[name] = snapshot
	}
}

// MarkCached marks one seed snapshot as present in the local cache.
func (m *SeedsManifest) MarkCached(name string) {
	if m == nil {
		return
	}
	snapshot, ok := m.Seeds.Snapshots[name]
	if !ok {
		return
	}
	snapshot.Cached = true
	m.Seeds.Snapshots[name] = snapshot
}
