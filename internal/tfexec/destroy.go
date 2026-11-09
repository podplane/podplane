// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StateResource identifies one managed instance and its recorded dependencies.
type StateResource struct {
	Address, Type string
	Attributes    map[string]json.RawMessage
	Dependencies  []string
}

// String reads a string attribute without exposing the rest of the state.
func (r StateResource) String(key string) string {
	var value string
	_ = json.Unmarshal(r.Attributes[key], &value)
	return value
}

// State retains the information needed to stage a destroy.
// Callers must not log its contents.
type State struct {
	Resources []StateResource
}

// CheckDefaultWorkspace prevents teardown from locking a different workspace's state.
func (c *CLI) CheckDefaultWorkspace(ctx context.Context, dir string) error {
	var out bytes.Buffer
	if err := c.eventProcess(ctx, dir, &out, &eventWriter{emit: func(e Event) { fmt.Fprintln(os.Stderr, e.Message) }}, "workspace", "show"); err != nil {
		return err
	}
	if workspace := strings.TrimSpace(out.String()); workspace != "default" {
		return fmt.Errorf("cluster delete requires the default Terraform workspace; current workspace is %q", workspace)
	}
	return nil
}

// ReadState reads the current state, without refreshing or modifying resources.
func (c *CLI) ReadState(ctx context.Context, dir string) (State, error) {
	var out bytes.Buffer
	if err := c.eventProcess(ctx, dir, &out, &eventWriter{emit: func(e Event) { fmt.Fprintln(os.Stderr, e.Message) }}, "state", "pull"); err != nil {
		return State{}, err
	}
	return parseState(out.Bytes())
}

// parseState extracts managed resources and their dependencies from raw engine state.
func parseState(raw []byte) (State, error) {
	var state struct {
		Version   int
		Resources []struct {
			Module, Mode, Type, Name string
			Instances                []struct {
				IndexKey     json.RawMessage `json:"index_key"`
				Attributes   map[string]json.RawMessage
				Dependencies []string
			}
		}
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, fmt.Errorf("read Terraform state: %w", err)
	}
	// Reject unfamiliar formats rather than risk treating managed resources as absent.
	if state.Version != 4 {
		return State{}, fmt.Errorf("unsupported Terraform state version %d", state.Version)
	}
	result := State{}
	for _, resource := range state.Resources {
		if resource.Mode != "managed" {
			continue
		}
		for _, instance := range resource.Instances {
			address := resource.Type + "." + resource.Name
			if resource.Module != "" {
				address = resource.Module + "." + address
			}
			if len(instance.IndexKey) > 0 && string(instance.IndexKey) != "null" {
				address += "[" + string(instance.IndexKey) + "]"
			}
			result.Resources = append(result.Resources, StateResource{address, resource.Type, instance.Attributes, instance.Dependencies})
		}
	}
	return result, nil
}

// PreserveDependencies expands a set of retained resources to include everything
// they depend on. Dependencies can name whole resources or modules.
func (s State) PreserveDependencies(keep map[string]bool) {
	for changed := true; changed; {
		changed = false
		for _, r := range s.Resources {
			if !keep[r.Address] {
				continue
			}
			for _, dependency := range r.Dependencies {
				for _, candidate := range s.Resources {
					if candidate.Address == dependency || strings.HasPrefix(candidate.Address, dependency+"[") || strings.HasPrefix(candidate.Address, dependency+".") {
						if !keep[candidate.Address] {
							keep[candidate.Address], changed = true, true
						}
					}
				}
			}
		}
	}
}

// DestroyExcept saves and checks a destroy plan before applying it. No action
// outside the explicit deletion set is allowed, even if targeting expands it.
// beforeApply, when non-nil, prepares resources only after plan validation; its
// failure prevents apply. lockHeld disables engine locking only when the caller
// already owns the backend lock across planning, cleanup, and apply.
func (c *CLI) DestroyExcept(ctx context.Context, dir string, state State, keep map[string]bool, lockHeld bool, beforeApply func(context.Context) error) error {
	state.PreserveDependencies(keep)
	allowed := map[string]bool{}
	for _, r := range state.Resources {
		if !keep[r.Address] {
			allowed[r.Address] = true
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	private, err := os.MkdirTemp("", "podplane-destroy-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(private) }()
	plan := filepath.Join(private, "plan")
	args := []string{"plan", "-destroy", "-input=false", "-out=" + plan}
	if lockHeld {
		args = append(args, "-lock=false")
	}
	if len(keep) > 0 {
		addresses := make([]string, 0, len(allowed))
		for address := range allowed {
			addresses = append(addresses, address)
		}
		sort.Strings(addresses)
		for _, address := range addresses {
			args = append(args, "-target="+address)
		}
	}
	if err := c.run(ctx, dir, args...); err != nil {
		return err
	}
	var out bytes.Buffer
	if err := c.eventProcess(ctx, dir, &out, &eventWriter{emit: func(e Event) { fmt.Fprintln(os.Stderr, e.Message) }}, "show", "-json", plan); err != nil {
		return err
	}
	var decoded struct {
		ResourceChanges []struct {
			Address, Mode string
			Change        struct{ Actions []string }
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		return err
	}
	for _, r := range decoded.ResourceChanges {
		if r.Mode != "managed" {
			continue
		}
		for _, action := range r.Change.Actions {
			if action != "no-op" && (action != "delete" || !allowed[r.Address]) {
				return fmt.Errorf("refusing destroy plan: unexpected %s of %s", action, r.Address)
			}
		}
	}
	if beforeApply != nil {
		if err := beforeApply(ctx); err != nil {
			return err
		}
	}
	args = []string{"apply", "-input=false"}
	if lockHeld {
		args = append(args, "-lock=false")
	}
	return c.run(ctx, dir, append(args, plan)...)
}
