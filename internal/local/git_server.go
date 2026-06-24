// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"net/http"

	"github.com/go-git/go-billy/v6/osfs"
	"github.com/go-git/go-git/v6/backend"
	"github.com/go-git/go-git/v6/plumbing/transport"

	"github.com/podplane/podplane/internal/deps"
)

// newLocalGitHandler serves cached Git repos through smart Git HTTP.
func newLocalGitHandler(depsCacheDir string) http.Handler {
	loader := transport.NewFilesystemLoader(osfs.New(deps.NewManager("", depsCacheDir).GitCacheDir()), true)
	handler := backend.New(loader)
	handler.Prefix = "/git"
	return handler
}
