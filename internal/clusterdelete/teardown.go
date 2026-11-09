// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/podplane/podplane/internal/tfexec"
)

// Actions selects successive teardown steps. Each step requires all earlier ones.
type Actions [5]bool

const (
	Infrastructure = iota
	ClusterBuckets
	StateBucket
	TerraformFiles
	ConfigFile
)

// CleanupProvider adds storage cleanup and server verification to VM teardown.
type CleanupProvider interface {
	Provider
	ServerInstances(context.Context, string) ([]Instance, error)
	EmptyBucket(context.Context, string) error
	DeleteBucket(context.Context, string) error
	LockState(context.Context, string, string) (StateLock, error)
}

// TeardownExecutor supplies state inspection and validated staged destruction.
type TeardownExecutor interface {
	Executor
	ReadState(context.Context, string) (tfexec.State, error)
	DestroyExcept(context.Context, string, tfexec.State, map[string]bool, bool, func(context.Context) error) error
}

// TeardownOptions supplies the checklist and cleanup boundaries for cluster delete.
type TeardownOptions struct {
	DeleteOptions
	Engine           TeardownExecutor
	Cloud            CleanupProvider
	StateBucketName  string
	StateLockKey     string
	CheckStateBucket func(context.Context) error
	ConfigPath       string
	Select           func([]string) (Actions, error)
}

// Teardown retains storage during the first destroy, waits for all writers to
// disappear, then empties and destroys selected storage. State and local files
// are removed only after the final state contains no managed resources.
func Teardown(ctx context.Context, opts TeardownOptions) (retErr error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, destroyTimeout)
	defer cancel()
	state, err := opts.Engine.ReadState(ctx, opts.StackDir)
	if err != nil {
		return err
	}
	_, buckets := storageResources(state)
	labels := []string{
		"Remove cluster compute and networking",
		"Delete cluster buckets and all data {" + strings.Join(buckets, ", ") + "}",
		"Delete state bucket {" + opts.StateBucketName + "}",
		"Delete generated cluster Terraform files",
		"Delete cluster config file",
	}
	actions, err := opts.Select(labels)
	if err != nil {
		return err
	}
	for i, checked := range actions {
		if checked && i > 0 && !actions[i-1] {
			return fmt.Errorf("cleanup selection has an unchecked prerequisite")
		}
	}
	if !actions[Infrastructure] {
		return nil
	}
	status := opts.Status
	if status == nil {
		status = func(string) {}
	}
	if actions[StateBucket] {
		if err := opts.CheckStateBucket(ctx); err != nil {
			return err
		}
	}
	// Validate local cleanup before touching remote infrastructure.
	var files []string
	if actions[TerraformFiles] {
		files, err = managedFiles(opts.StackDir)
		if err != nil {
			return err
		}
	}
	lock, err := opts.Cloud.LockState(ctx, opts.StateBucketName, opts.StateLockKey)
	if err != nil {
		return err
	}
	unlocked := false
	unlock := func() error {
		unlocked = true
		// Cancellation must not leave a lock behind after engine cleanup.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return lock.Unlock(cleanup)
	}
	defer func() {
		if !unlocked {
			retErr = errors.Join(retErr, unlock())
		}
	}()
	state, err = opts.Engine.ReadState(ctx, opts.StackDir)
	if err != nil {
		return err
	}
	keep, currentBuckets := storageResources(state)
	if !slices.Equal(buckets, currentBuckets) {
		return fmt.Errorf("cluster buckets changed during confirmation; rerun cluster delete to review them")
	}
	opts.Executor = &stagedExecutor{TeardownExecutor: opts.Engine, state: state, keep: keep}
	opts.Provider = opts.Cloud
	opts.AfterDestroy = func(ctx context.Context) error {
		status("Waiting for Nstance servers to terminate before storage cleanup.")
		deadline, cancel := context.WithTimeout(ctx, opts.WaitTimeout)
		defer cancel()
		for {
			servers, err := opts.Cloud.ServerInstances(deadline, opts.ClusterConfig.Cluster.ID)
			if err != nil {
				return err
			}
			if len(servers) == 0 {
				break
			}
			select {
			case <-deadline.Done():
				return fmt.Errorf("server instances still exist; buckets retained: %w", deadline.Err())
			case <-time.After(opts.PollInterval):
			}
		}
		// A server could have launched a workload VM while it was shutting down.
		instances, err := opts.Cloud.ManagedInstances(ctx, opts.ClusterConfig.Cluster.ID)
		if err != nil {
			return err
		}
		if len(instances) != 0 {
			if err := opts.Cloud.Terminate(ctx, instances); err != nil {
				return err
			}
			instances, err = waitForManagedInstances(ctx, opts.Cloud, opts.ClusterConfig.Cluster.ID, opts.WaitTimeout, opts.PollInterval)
			if err != nil {
				return err
			}
			if len(instances) != 0 {
				return fmt.Errorf("workload instances still exist; buckets retained")
			}
		}
		remaining, err := opts.Engine.ReadState(ctx, opts.StackDir)
		if err != nil {
			return err
		}
		for _, resource := range remaining.Resources {
			if !keep[resource.Address] {
				return fmt.Errorf("infrastructure resource %s remains; buckets retained", resource.Address)
			}
		}
		if !actions[ClusterBuckets] {
			status("Cluster buckets, their dependencies, state, and local files were retained.")
			return nil
		}
		if err := opts.Engine.DestroyExcept(ctx, opts.StackDir, remaining, map[string]bool{}, true, func(ctx context.Context) error {
			for _, bucket := range buckets {
				status("Emptying cluster bucket " + bucket + " (including old versions).")
				if err := opts.Cloud.EmptyBucket(ctx, bucket); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		remaining, err = opts.Engine.ReadState(ctx, opts.StackDir)
		if err != nil {
			return err
		}
		if len(remaining.Resources) != 0 {
			return fmt.Errorf("managed resources remain; state and local files retained")
		}
		if actions[StateBucket] {
			status("Removing state bucket " + opts.StateBucketName + ".")
			if err := lock.EmptyBucket(ctx); err != nil {
				return err
			}
			// Never retry unlock or empty the bucket after this point: another
			// client may acquire the lock and write new state as soon as we release it.
			if err := unlock(); err != nil {
				return err
			}
			if err := opts.Cloud.DeleteBucket(ctx, opts.StateBucketName); err != nil {
				return fmt.Errorf("state bucket retained; it may have been reused by another client (contents were not deleted again): %w", err)
			}
		}
		for _, file := range files {
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("infrastructure deleted; remove local file %s manually: %w", file, err)
			}
		}
		if actions[ConfigFile] {
			if err := os.Remove(opts.ConfigPath); err != nil {
				return fmt.Errorf("infrastructure deleted; remove config manually: %w", err)
			}
		}
		return nil
	}
	return Delete(ctx, opts.DeleteOptions)
}

// stagedExecutor adapts VM teardown to a destroy that preserves storage dependencies.
type stagedExecutor struct {
	TeardownExecutor
	state tfexec.State
	keep  map[string]bool
}

// Destroy limits the initial teardown to resources outside the retained set.
func (s *stagedExecutor) Destroy(ctx context.Context, dir string, _ bool) error {
	return s.DestroyExcept(ctx, dir, s.state, s.keep, true, nil)
}

// storageResources identifies buckets and everything needed to keep retained data usable.
func storageResources(state tfexec.State) (map[string]bool, []string) {
	keep := map[string]bool{}
	names := map[string]bool{}
	for _, resource := range state.Resources {
		// Preserve bucket settings/objects, seed resources, and secret backends,
		// not just the bucket itself. Data must remain usable when retained.
		if strings.HasPrefix(resource.Type, "aws_s3_") || strings.HasPrefix(resource.Type, "google_storage_") ||
			strings.HasPrefix(resource.Type, "aws_kms_") || strings.HasPrefix(resource.Type, "aws_secretsmanager_") ||
			strings.HasPrefix(resource.Type, "google_kms_") || strings.HasPrefix(resource.Type, "google_secret_manager_") ||
			strings.HasPrefix(resource.Type, "podplane_") && (resource.String("bucket") != "" || strings.Contains(resource.Type, "ca")) {
			keep[resource.Address] = true
		}
		var name string
		switch resource.Type {
		case "aws_s3_bucket":
			name = resource.String("bucket")
		case "google_storage_bucket":
			name = resource.String("name")
		}
		if name != "" {
			names[name] = true
		}
	}
	state.PreserveDependencies(keep)
	buckets := make([]string, 0, len(names))
	for name := range names {
		buckets = append(buckets, name)
	}
	sort.Strings(buckets)
	return keep, buckets
}

// managedFiles permits cleanup only of regular files bearing Podplane's generated header.
func managedFiles(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "podplane.cluster.*.tf"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing to remove non-regular file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(string(data), "# Podplane managed OpenTofu/Terraform file.\n") {
			return nil, fmt.Errorf("refusing to remove unmanaged file %s", path)
		}
	}
	return paths, nil
}
