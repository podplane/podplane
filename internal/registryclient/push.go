// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package registryclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/podplane/ocimage/pkg/store"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/kubectl"
)

// Options configures a registry image push.
type Options struct {
	Config     *config.Config
	Source     string
	Remote     string
	StoreRoot  string
	Docker     string
	Context    string
	Kubeconfig string
	Stderr     io.Writer
	Verbose    bool
	Confirm    func(string) (bool, error)
}

// Push pushes a local image to the current Podplane cluster registry.
func Push(ctx context.Context, opts Options) (string, error) {
	if opts.Config == nil {
		return "", fmt.Errorf("config is required")
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if opts.StoreRoot == "" {
		return "", fmt.Errorf("registry store root is required")
	}
	clusterID, local, err := kubectl.ClusterIDFromContext(opts.Context, opts.Kubeconfig)
	if err != nil {
		return "", err
	}
	summary, err := opts.Config.ClusterSummary(clusterID, local)
	if err != nil {
		return "", err
	}
	if summary.ID == "" {
		return "", fmt.Errorf("cluster summary for %q is not cached; run `podplane login -f <podplane.cluster.jsonc>` for this cluster", clusterID)
	}
	if summary.Registry.Hostname == "" {
		return "", fmt.Errorf("cluster %q has no cluster.registry.hostname configured; rerun login/local start after configuring the registry hostname", clusterID)
	}
	if err := ensureZotRegistryReady(opts.Context, opts.Kubeconfig); err != nil {
		return "", err
	}

	source, err := sourceRef(opts.Source)
	if err != nil {
		return "", fmt.Errorf("parse local image %q: %w", opts.Source, err)
	}
	remoteRef, err := remoteRef(summary.Registry.Hostname, source, opts.Remote)
	if err != nil {
		return "", err
	}
	token, err := resolvePushToken(opts.Config, clusterID, local, opts.Context, opts.Kubeconfig)
	if err != nil {
		return "", err
	}

	st := store.Store{Root: opts.StoreRoot}
	if err := ensureStoreImage(ctx, st, source, opts); err != nil {
		return "", err
	}
	_, _ = fmt.Fprintln(opts.Stderr, "Connecting to cluster registry...")
	localPort, stopForward, err := startRegistryPortForward(ctx, opts.Context, opts.Kubeconfig, opts.Stderr, opts.Verbose)
	if err != nil {
		return "", err
	}
	defer stopForward()
	_, _ = fmt.Fprintln(opts.Stderr, "Connected to cluster registry.")
	_, _ = fmt.Fprintf(opts.Stderr, "Pushing %s...\n", sourceDisplay(source))

	pfRef, err := name.NewTag("127.0.0.1:"+localPort+"/"+remoteRef.Context().RepositoryStr()+":"+remoteRef.Identifier(), name.Insecure)
	if err != nil {
		return "", fmt.Errorf("build port-forward registry reference: %w", err)
	}
	auth := authn.FromConfig(authn.AuthConfig{RegistryToken: token})
	if err := st.Push(ctx, source, store.PushOptions{Destination: pfRef, RemoteOptions: []remote.Option{remote.WithAuth(auth)}}); err != nil {
		return "", fmt.Errorf("push image: %w", err)
	}
	return remoteRef.Name(), nil
}

// ensureStoreImage ensures source exists in the Podplane local registry cache.
func ensureStoreImage(ctx context.Context, st store.Store, source name.Tag, opts Options) error {
	display := sourceDisplay(source)
	if _, err := st.Descriptor(ctx, source); err == nil {
		return nil
	}
	if opts.Docker == "" {
		return fmt.Errorf("image %s was not found in Podplane's local registry cache; run `podplane build -t %s` first", display, display)
	}
	docker, err := exec.LookPath(opts.Docker)
	if err != nil {
		return fmt.Errorf("image %s was not found in Podplane's local registry cache, and %q was not found", display, opts.Docker)
	}
	dockerRef, err := dockerImage(opts.Source, source, ctx, docker)
	if err != nil {
		return fmt.Errorf("image %s was not found in Podplane's local registry cache or Docker's local image store", display)
	}
	if opts.Confirm != nil {
		ok, err := opts.Confirm(fmt.Sprintf("Image %s was not found in Podplane's local registry cache, but %s exists in Docker. Push the Docker image instead?", display, dockerRef))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("push cancelled")
		}
	}
	return loadDockerImage(ctx, st, docker, dockerRef, source)
}

// sourceDisplay returns a registry-less app image name for user-facing output.
func sourceDisplay(source name.Tag) string {
	return source.Context().RepositoryStr() + ":" + source.Identifier()
}

// dockerImage returns the first Docker image candidate present locally.
func dockerImage(sourceInput string, source name.Tag, ctx context.Context, docker string) (string, error) {
	for _, candidate := range dockerImageCandidates(sourceInput, source) {
		if err := inspectDockerImage(ctx, docker, candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("docker image not found")
}

// dockerImageCandidates returns likely Docker image names for a Podplane app source.
func dockerImageCandidates(sourceInput string, source name.Tag) []string {
	repo, tag := splitRepoTag(sourceInput)
	if tag == "" {
		tag = source.Identifier()
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}
	add(repo + ":" + tag)
	storeRepo := source.Context().RepositoryStr()
	add(storeRepo + ":" + source.Identifier())
	if strings.HasPrefix(storeRepo, "apps/") {
		add(strings.TrimPrefix(storeRepo, "apps/") + ":" + source.Identifier())
	}
	return out
}

// inspectDockerImage checks whether ref exists in Docker's local image store.
func inspectDockerImage(ctx context.Context, docker string, ref string) error {
	cmd := exec.CommandContext(ctx, docker, "image", "inspect", ref)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker image inspect: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// loadDockerImage saves ref from Docker and imports it into the ocimage store.
func loadDockerImage(ctx context.Context, st store.Store, docker string, ref string, dst name.Tag) error {
	tmp, err := os.CreateTemp("", "podplane-push-*.tar")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer func() { _ = os.Remove(path) }()
	if err := tmp.Close(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, docker, "image", "save", "--output", path, ref)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker image save: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	src, err := name.NewTag(ref, name.WeakValidation)
	if err != nil {
		return err
	}
	return st.LoadDockerArchives(ctx, dst, []store.PlatformArchive{{Path: path, Src: src}})
}
