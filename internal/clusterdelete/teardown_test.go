// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/tfexec"
)

// TestTeardown checks cleanup ordering, retention choices, and preservation after failures.
func TestTeardown(t *testing.T) {
	for _, scenario := range []string{"all", "late VM", "keep buckets", "keep state", "none", "empty failure", "destroy failure", "storage plan failure", "server remains", "cancel", "invalid selection", "lock conflict", "bucket reused", "unlock failure", "cancel cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			config := filepath.Join(dir, "cluster.jsonc")
			generated := filepath.Join(dir, "podplane.cluster.main.tf")
			userFile := filepath.Join(dir, "user.tf")
			for path, content := range map[string]string{config: "{}", generated: "# Podplane managed OpenTofu/Terraform file.\n", userFile: "user content"} {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			engine := &teardownEngine{state: tfexec.State{Resources: []tfexec.StateResource{
				{Address: "random_id.name", Type: "random_id"},
				{Address: "aws_s3_bucket.netsy", Type: "aws_s3_bucket", Attributes: map[string]json.RawMessage{"bucket": json.RawMessage(`"netsy"`)}, Dependencies: []string{"random_id.name"}},
				{Address: "aws_autoscaling_group.server", Type: "aws_autoscaling_group"},
			}}}
			cloud := &teardownCloud{engine: engine, objects: map[string]bool{"netsy": true, "tfstate": true}}
			actions := Actions{true, true, true, true, true}
			switch scenario {
			case "keep buckets":
				actions = Actions{true}
			case "keep state":
				actions = Actions{true, true}
			case "none":
				actions = Actions{}
			case "invalid selection":
				actions = Actions{true, false, true}
			case "empty failure":
				cloud.fail = true
			case "destroy failure":
				engine.fail = true
			case "storage plan failure":
				engine.storagePlanFails = true
			case "server remains":
				cloud.servers = true
			case "late VM":
				cloud.lateVM = true
			case "lock conflict":
				cloud.lockConflict = true
			case "bucket reused":
				cloud.reuse = true
			case "unlock failure":
				cloud.unlockFails = true
			case "cancel cleanup":
				cloud.cancel = cancel
			}
			err := Teardown(ctx, TeardownOptions{
				DeleteOptions: DeleteOptions{ClusterConfig: &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "test"}}, StackDir: dir,
					WaitTimeout: time.Millisecond, PollInterval: time.Millisecond, Confirm: func(string) (bool, error) { return true, nil }},
				Engine: engine, Cloud: cloud, StateBucketName: "tfstate", ConfigPath: config,
				CheckStateBucket: func(context.Context) error { return nil },
				Select: func(labels []string) (Actions, error) {
					if len(labels) != 5 || !strings.Contains(labels[1], "{netsy}") || !strings.Contains(labels[2], "{tfstate}") {
						t.Fatalf("checklist = %v", labels)
					}
					if scenario == "cancel" {
						return Actions{}, errors.New("cancelled")
					}
					return actions, nil
				},
			})
			wantError := slices.Contains([]string{"empty failure", "destroy failure", "storage plan failure", "server remains", "cancel", "invalid selection", "lock conflict", "bucket reused", "unlock failure", "cancel cleanup"}, scenario)
			if (err != nil) != wantError {
				t.Fatalf("error = %v", err)
			}
			if scenario == "all" || scenario == "late VM" {
				if len(engine.state.Resources) != 0 || cloud.objects["netsy"] || cloud.objects["tfstate"] || !cloud.stateDeleted {
					t.Fatal("cleanup incomplete")
				}
				for _, path := range []string{config, generated} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("file not removed: %s", path)
					}
				}
			} else {
				if (!cloud.objects["tfstate"] && scenario != "unlock failure") || cloud.stateDeleted {
					t.Fatal("state bucket deleted without completed prerequisites")
				}
				for _, path := range []string{config, generated} {
					if _, err := os.Stat(path); err != nil {
						t.Fatal("recovery file removed")
					}
				}
				if !slices.Contains([]string{"keep state", "bucket reused", "unlock failure"}, scenario) && !cloud.objects["netsy"] {
					t.Fatal("bucket contents unexpectedly deleted")
				}
			}
			if engine.locked && scenario != "unlock failure" {
				t.Fatal("teardown left its state lock behind")
			}
			if data, err := os.ReadFile(userFile); err != nil || string(data) != "user content" {
				t.Fatal("user Terraform file modified")
			}
			if scenario == "keep buckets" && len(engine.state.Resources) != 2 {
				t.Fatal("storage or its dependency lost")
			}
		})
	}
}

// teardownEngine models resources retained by successive destroy operations.
type teardownEngine struct {
	state            tfexec.State
	fail             bool
	storagePlanFails bool
	locked           bool
}

// ReadState returns the resources still managed by the fake engine.
func (e *teardownEngine) ReadState(context.Context, string) (tfexec.State, error) {
	return e.state, nil
}

// OutputJSON represents a stack without active Nstance shards.
func (e *teardownEngine) OutputJSON(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}

// Destroy rejects accidental use of unrestricted destruction.
func (e *teardownEngine) Destroy(context.Context, string, bool) error {
	return errors.New("unrestricted destroy must not run")
}

// DestroyExcept removes only resources outside the retained set.
func (e *teardownEngine) DestroyExcept(ctx context.Context, _ string, _ tfexec.State, keep map[string]bool, lockHeld bool, beforeApply func(context.Context) error) error {
	if !lockHeld || !e.locked {
		return errors.New("destroy outside backend lock")
	}
	if e.fail {
		return errors.New("destroy failed")
	}
	if e.storagePlanFails && len(keep) == 0 {
		return errors.New("bucket lifecycle prevents destruction")
	}
	if beforeApply != nil {
		if err := beforeApply(ctx); err != nil {
			return err
		}
	}
	var remaining []tfexec.StateResource
	for _, resource := range e.state.Resources {
		if keep[resource.Address] {
			remaining = append(remaining, resource)
		}
	}
	e.state.Resources = remaining
	return nil
}

// teardownCloud models stored data and writers that may outlive the first destroy.
type teardownCloud struct {
	fakeProvider
	engine                              *teardownEngine
	objects                             map[string]bool
	fail, servers, stateDeleted, lateVM bool
	lockConflict, reuse, unlockFails    bool
	cancel                              context.CancelFunc
}

// LockState models exclusive backend ownership, including a competing operator.
func (p *teardownCloud) LockState(context.Context, string, string) (StateLock, error) {
	if p.lockConflict || p.engine.locked {
		return nil, errors.New("state already locked")
	}
	p.engine.locked = true
	return teardownLock{p}, nil
}

// teardownLock exposes storage cleanup only while this client owns the backend lock.
type teardownLock struct{ cloud *teardownCloud }

// EmptyBucket keeps the lock while removing state contents.
func (l teardownLock) EmptyBucket(ctx context.Context) error {
	return l.cloud.EmptyBucket(ctx, "tfstate")
}

// Unlock optionally lets another client acquire the bucket immediately after release.
func (l teardownLock) Unlock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.cloud.unlockFails {
		return errors.New("lock removal failed")
	}
	l.cloud.engine.locked = false
	if l.cloud.reuse {
		l.cloud.objects["tfstate"] = true
	}
	return nil
}

// ServerInstances simulates surviving servers and a late workload launch.
func (p *teardownCloud) ServerInstances(ctx context.Context, _ string) ([]Instance, error) {
	if p.cancel != nil {
		p.cancel()
		return nil, ctx.Err()
	}
	if p.lateVM {
		p.managed = []Instance{{ID: "late-workload"}}
	}
	if p.servers {
		return []Instance{{ID: "server"}}, nil
	}
	return nil, nil
}

// EmptyBucket rejects cleanup while writers or state-managed infrastructure remain.
func (p *teardownCloud) EmptyBucket(_ context.Context, name string) error {
	if !p.engine.locked {
		return errors.New("emptying outside backend lock")
	}
	if len(p.managed) > 0 {
		return errors.New("workload VM still writing")
	}
	for _, r := range p.engine.state.Resources {
		if r.Type == "aws_autoscaling_group" || name == "tfstate" {
			return errors.New("emptied before infrastructure destruction")
		}
	}
	if p.fail {
		return errors.New("object is locked")
	}
	p.objects[name] = false
	return nil
}

// DeleteBucket permits only the empty state bucket to be removed outside Terraform.
func (p *teardownCloud) DeleteBucket(_ context.Context, name string) error {
	if name != "tfstate" || p.objects[name] || p.engine.locked {
		return errors.New("unexpected bucket deletion")
	}
	p.stateDeleted = true
	return nil
}
