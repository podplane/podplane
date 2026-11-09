// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterdelete

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/tfexec"
)

// TestTeardownPromptInterrupt checks that SIGINT exits while plain input is blocked.
func TestTeardownPromptInterrupt(t *testing.T) {
	if os.Getenv("PODPLANE_TEST_DELETE_INTERRUPT") == "1" {
		_ = Teardown(t.Context(), TeardownOptions{
			Engine: &teardownEngine{},
			Select: func([]string) (Actions, error) {
				_, _ = os.Stdout.WriteString("prompt ready\n")
				_, err := bufio.NewReader(os.Stdin).ReadString('\n')
				return Actions{}, err
			},
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTeardownPromptInterrupt$")
	cmd.Env = append(os.Environ(), "PODPLANE_TEST_DELETE_INTERRUPT=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "prompt ready\n" {
		t.Fatalf("prompt not reached: %q, %v", line, err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("interrupted deletion exited successfully")
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("SIGINT did not exit the blocked deletion prompt; forced exit: %v", <-done)
	}
}

// TestTeardown checks cleanup ordering, retention choices, and preservation after failures.
func TestTeardown(t *testing.T) {
	for _, scenario := range []string{"all", "custom", "custom declined", "custom changed", "late VM", "keep buckets", "keep state", "keep config", "keep files", "none", "empty failure", "destroy failure", "storage plan failure", "server remains", "cancel", "invalid selection", "lock conflict", "bucket reused", "unlock failure", "cancel cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			config := filepath.Join(dir, "cluster.jsonc")
			generated := filepath.Join(dir, "podplane.cluster.main.tf")
			userFile := filepath.Join(dir, "user.tf")
			for path, content := range map[string]string{config: "{}", generated: `# Podplane managed OpenTofu/Terraform file.
resource "random_id" "name" {}
resource "aws_s3_bucket" "netsy" {}
resource "aws_autoscaling_group" "server" {}
module "cluster" { source = "./module" }
`, userFile: "user content"} {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			support := []string{".podplane/cluster-backend.hcl", ".podplane/vmconfig_nst.json", ".podplane/future/extra.json", ".terraform/providers/link", ".terraform.lock.hcl"}
			for _, name := range append(slices.Clone(support), ".podplane/cluster-schema.json", "oidc/.podplane/oidc-schema.json") {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("support"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			engine := &teardownEngine{state: tfexec.State{Resources: []tfexec.StateResource{
				{Address: "random_id.name", Type: "random_id"},
				{Address: "aws_s3_bucket.netsy", Type: "aws_s3_bucket", Attributes: map[string]json.RawMessage{"bucket": json.RawMessage(`"netsy"`)}, Dependencies: []string{"random_id.name"}},
				{Address: "aws_autoscaling_group.server", Type: "aws_autoscaling_group"},
				{Address: `module.cluster["zone.a"].module.child.aws_instance.server[0]`, Type: "aws_instance"},
			}}}
			if strings.HasPrefix(scenario, "custom") {
				engine.state.Resources = append(engine.state.Resources,
					tfexec.StateResource{Address: "random_id.name_extra[0]", Type: "random_id"},
					tfexec.StateResource{Address: `module.cluster_extra["a.b"].aws_instance.custom`, Type: "aws_instance"})
			}
			cloud := &teardownCloud{engine: engine, objects: map[string]bool{"netsy": true, "tfstate": true}}
			actions := Actions{true, true, true, true, true}
			switch scenario {
			case "keep buckets":
				actions = Actions{true}
			case "keep state":
				actions = Actions{true, true}
			case "keep config":
				actions = Actions{true, true, true, true}
			case "keep files":
				actions = Actions{true, true, true}
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
			warned := false
			err := Teardown(ctx, TeardownOptions{
				DeleteOptions: DeleteOptions{ClusterConfig: &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "test"}}, StackDir: dir,
					WaitTimeout: time.Millisecond, PollInterval: time.Millisecond, Confirm: func(string) (bool, error) { return true, nil }},
				Engine: engine, Cloud: cloud, StateBucketName: "tfstate", ConfigPath: config,
				CheckStateBucket: func(context.Context) error { return nil },
				ConfirmCustom: func(message string) (bool, error) {
					warned = true
					if !strings.HasPrefix(scenario, "custom") ||
						!strings.Contains(message, "random_id.name_extra[0]") ||
						!strings.Contains(message, `module.cluster_extra["a.b"].aws_instance.custom`) ||
						strings.Contains(message, `module.cluster["zone.a"]`) {
						t.Fatalf("unexpected custom warning: %s", message)
					}
					if engine.locked || !cloud.objects["netsy"] || cloud.stateDeleted {
						t.Fatal("warning followed destructive work")
					}
					if scenario == "custom changed" {
						engine.state.Resources = append(engine.state.Resources, tfexec.StateResource{Address: "random_id.new", Type: "random_id"})
					}
					return scenario != "custom declined", nil
				},
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
			if warned != strings.HasPrefix(scenario, "custom") {
				t.Fatalf("custom warning shown = %t", warned)
			}
			wantError := slices.Contains([]string{"custom declined", "custom changed", "empty failure", "destroy failure", "storage plan failure", "server remains", "cancel", "invalid selection", "lock conflict", "bucket reused", "unlock failure", "cancel cleanup"}, scenario)
			if (err != nil) != wantError {
				t.Fatalf("error = %v", err)
			}
			if scenario == "custom declined" && len(engine.state.Resources) != 6 ||
				scenario == "custom changed" && len(engine.state.Resources) != 7 {
				t.Fatal("unapproved custom resources were destroyed")
			}
			if scenario == "all" || scenario == "custom" || scenario == "late VM" || scenario == "keep config" || scenario == "keep files" {
				if len(engine.state.Resources) != 0 || cloud.objects["netsy"] || cloud.objects["tfstate"] || !cloud.stateDeleted {
					t.Fatal("cleanup incomplete")
				}
				var removed []string
				if actions[TerraformFiles] {
					removed = append(removed, generated)
				} else if _, err := os.Stat(generated); err != nil {
					t.Fatal("unchecked Terraform cleanup removed generated files")
				}
				if actions[ConfigFile] {
					removed = append(removed, config)
				}
				for _, path := range removed {
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
			for _, name := range support {
				_, statErr := os.Stat(filepath.Join(dir, name))
				wantRemoved := err == nil && actions[TerraformFiles]
				if os.IsNotExist(statErr) != wantRemoved {
					t.Fatalf("support file %s: %v, want removed=%t", name, statErr, wantRemoved)
				}
			}
			for _, name := range []string{"cluster.jsonc", ".podplane/cluster-schema.json", ".podplane"} {
				_, statErr := os.Stat(filepath.Join(dir, name))
				if os.IsNotExist(statErr) != (err == nil && actions[ConfigFile]) {
					t.Fatalf("config support %s: %v", name, statErr)
				}
			}
			if data, err := os.ReadFile(filepath.Join(dir, "oidc/.podplane/oidc-schema.json")); err != nil || string(data) != "support" {
				t.Fatal("OIDC support files modified")
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
