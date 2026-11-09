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

	"github.com/podplane/podplane/internal/clusterconfig"
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
	ConfirmCustom    ConfirmFunc
	// Run presents the already-confirmed execution; nil executes directly.
	Run      func(context.Context, func(context.Context) error) error
	Progress func(tfexec.Event)
}

// CleanupTasks lists the teardown steps in execution order.
func CleanupTasks() []string {
	return []string{"Stop workload instances", "Destroy compute and networking", "Delete cluster buckets", "Delete state bucket", "Remove Terraform files", "Remove cluster config"}
}

// Teardown retains storage during the first destroy, waits for all writers to
// disappear, then empties and destroys selected storage. State and local files
// are removed only after the final state contains no managed resources.
func Teardown(ctx context.Context, opts TeardownOptions) (retErr error) {
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
	custom, err := customResources(opts.StackDir, state)
	if err != nil {
		return err
	}
	if len(custom) != 0 {
		if opts.ConfirmCustom == nil {
			return fmt.Errorf("custom resources require explicit confirmation")
		}
		approved, err := opts.ConfirmCustom("Warning: cluster deletion also destroys these custom managed resources (unless retained with storage):\n  " + strings.Join(custom, "\n  "))
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("cluster deletion cancelled")
		}
	}
	run := func(ctx context.Context) error { return executeTeardown(ctx, opts, actions, buckets, custom) }
	if opts.Run != nil {
		return opts.Run(ctx, run)
	}
	return run(ctx)
}

// executeTeardown holds the backend lock across validated destruction and cleanup.
func executeTeardown(ctx context.Context, opts TeardownOptions, actions Actions, buckets, custom []string) (retErr error) {
	var err error
	tasks := CleanupTasks()
	progress := func(index int, state string) {
		if opts.Progress != nil {
			stage := "Delete"
			if index >= 2 {
				stage = "Cleanup"
			}
			opts.Progress(tfexec.Event{Stage: stage, Type: "task_" + state, Address: tasks[index], Message: tasks[index] + ": " + state})
		}
	}
	for i := 2; i < len(tasks); i++ {
		if !actions[i-1] && opts.Progress != nil {
			opts.Progress(tfexec.Event{Type: "task_skip", Address: tasks[i]})
		}
	}
	// Inline confirmation blocks on stdin. Keep default signal handling until
	// approval; only teardown needs graceful cancellation to release its state lock.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
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
	state, err := opts.Engine.ReadState(ctx, opts.StackDir)
	if err != nil {
		return err
	}
	currentCustom, err := customResources(opts.StackDir, state)
	if err != nil {
		return err
	}
	if !slices.Equal(custom, currentCustom) {
		return fmt.Errorf("custom resources changed during confirmation; rerun cluster delete to review them")
	}
	keep, currentBuckets := storageResources(state)
	if !slices.Equal(buckets, currentBuckets) {
		return fmt.Errorf("cluster buckets changed during confirmation; rerun cluster delete to review them")
	}
	progress(0, "start")
	opts.Executor = &stagedExecutor{TeardownExecutor: opts.Engine, state: state, keep: keep, progress: progress}
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
			progress(1, "complete")
			status("Cluster buckets, their dependencies, state, and local files were retained.")
			return nil
		}
		progress(1, "complete")
		progress(2, "start")
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
		progress(2, "complete")
		if actions[StateBucket] {
			progress(3, "start")
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
			progress(3, "complete")
		}
		if actions[TerraformFiles] {
			progress(4, "start")
		}
		for _, file := range files {
			if err := os.RemoveAll(file); err != nil {
				return fmt.Errorf("infrastructure deleted; remove local file %s manually: %w", file, err)
			}
		}
		if actions[TerraformFiles] {
			progress(4, "complete")
		}
		if actions[ConfigFile] {
			progress(5, "start")
			if err := os.Remove(opts.ConfigPath); err != nil {
				return fmt.Errorf("infrastructure deleted; remove config manually: %w", err)
			}
			if err := os.RemoveAll(filepath.Join(opts.StackDir, ".podplane")); err != nil {
				return fmt.Errorf("remove cluster support directory: %w", err)
			}
			progress(5, "complete")
		}
		return nil
	}
	return Delete(ctx, opts.DeleteOptions)
}

// stagedExecutor adapts VM teardown to a destroy that preserves storage dependencies.
type stagedExecutor struct {
	TeardownExecutor
	state    tfexec.State
	keep     map[string]bool
	progress func(int, string)
}

// Destroy limits the initial teardown to resources outside the retained set.
func (s *stagedExecutor) Destroy(ctx context.Context, dir string, _ bool) error {
	s.progress(0, "complete")
	s.progress(1, "start")
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

// managedFiles selects marked Terraform and backend files and the support directory's
// contents, retaining the schema until the config is removed.
func managedFiles(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "podplane.cluster.*.tf"))
	if err != nil {
		return nil, err
	}
	backend := filepath.Join(dir, "podplane.cluster.backend.hcl")
	if _, err := os.Lstat(backend); err == nil {
		paths = append(paths, backend)
	} else if !os.IsNotExist(err) {
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
	paths = append(paths, filepath.Join(dir, ".terraform"), filepath.Join(dir, ".terraform.lock.hcl"))
	support := filepath.Join(dir, ".podplane")
	info, err := os.Lstat(support)
	if os.IsNotExist(err) {
		return paths, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("refusing to clean non-directory support path %s", support)
	}
	entries, err := os.ReadDir(support)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		path := filepath.Join(support, entry.Name())
		if path != filepath.Join(dir, clusterconfig.SchemaFileName) {
			paths = append(paths, path)
		}
	}
	return paths, nil
}
