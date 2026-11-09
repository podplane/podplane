// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Change is a resource's planned operation and redacted review information.
type Change struct {
	Address string
	Actions []string
	Details string
}

// Action describes the complete operation, including replacement order.
func (c Change) Action() string {
	if len(c.Actions) > 1 {
		return "Replace"
	}
	if len(c.Actions) == 0 {
		return "No change"
	}
	return strings.ToUpper(c.Actions[0][:1]) + c.Actions[0][1:]
}

// Plan contains only reviewable, redacted information, not the saved plan file.
type Plan struct {
	Changes    []Change
	Outputs    string
	HasChanges bool
}

// Event carries plan-and-apply progress: stage transitions, engine messages,
// and resource lifecycle events.
// Emit callbacks are serialized, including stdout and stderr messages.
type Event struct {
	Stage   string
	Type    string
	Address string
	Action  string
	Message string
}

// PlanAndApply initializes, saves and reviews a plan, then applies precisely that plan.
// The caller owns approval; a rejected plan is never passed to apply. Plan files
// live in a private temporary directory and are removed on every return path.
func (c *CLI) PlanAndApply(ctx context.Context, dir, backend string, emit func(Event), review func(Plan) (bool, error)) error {
	private, err := os.MkdirTemp("", "podplane-plan-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(private) }()
	planPath := filepath.Join(private, "plan")
	var mu sync.Mutex
	send := func(e Event) { mu.Lock(); defer mu.Unlock(); emit(e) }
	send(Event{Stage: "Init"})
	if err := c.eventCommand(ctx, dir, false, send, "init", "-input=false", "-no-color", "-backend-config="+backend); err != nil {
		return err
	}
	send(Event{Stage: "Plan"})
	err = c.eventCommand(ctx, dir, true, send, "plan", "-json", "-input=false", "-detailed-exitcode", "-out="+planPath)
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 2) {
		return err
	}
	var out bytes.Buffer
	if err := c.eventProcess(ctx, dir, &out, &eventWriter{emit: send}, "show", "-json", planPath); err != nil {
		return err
	}
	plan, err := decodePlan(out.Bytes())
	if err != nil {
		return fmt.Errorf("read saved plan: %w", err)
	}
	send(Event{Stage: "Review"})
	ok, err := review(plan)
	if err != nil {
		return err
	}
	if !plan.HasChanges {
		return nil
	}
	if !ok {
		return fmt.Errorf("apply cancelled; plan was not applied")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	send(Event{Stage: "Apply"})
	return c.eventCommand(ctx, dir, true, send, "apply", "-json", "-input=false", planPath)
}

// eventCommand translates both output streams into events and flushes trailing records.
func (c *CLI) eventCommand(ctx context.Context, dir string, structured bool, emit func(Event), args ...string) error {
	stdout := &eventWriter{structured: structured, emit: emit}
	stderr := &eventWriter{emit: emit}
	err := c.eventProcess(ctx, dir, stdout, stderr, args...)
	stdout.flush()
	stderr.flush()
	if stdout.err != nil {
		return stdout.err
	}
	return err
}

// eventProcess runs the engine with private configuration and graceful cancellation.
func (c *CLI) eventProcess(ctx context.Context, dir string, stdout io.Writer, stderr *eventWriter, args ...string) error {
	config, cleanup, err := c.commandConfig()
	if err != nil {
		return err
	}
	defer cleanup()
	defer stderr.flush()
	cmd := exec.Command(c.binary, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, stdout, stderr
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	if config != "" {
		cmd.Env = append(withoutEnv(cmd.Env, "TF_CLI_CONFIG_FILE"), "TF_CLI_CONFIG_FILE="+config)
	}
	if err := runProcess(ctx, cmd, func(message string) { stderr.emit(Event{Type: "log", Message: message}) }); err != nil {
		return fmt.Errorf("OpenTofu/Terraform %s: %w", args[0], err)
	}
	return nil
}

// eventWriter accepts arbitrary pipe chunks, not just newline-aligned writes.
// Unknown event types retain their message. Outputs are deliberately not logged.
type eventWriter struct {
	structured bool
	emit       func(Event)
	pending    []byte
	err        error
}

// Write assembles bounded newline-delimited records from arbitrary pipe chunks.
func (w *eventWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		line, rest, found := bytes.Cut(p, []byte{'\n'})
		if len(w.pending)+len(line) > 4<<20 {
			w.err = fmt.Errorf("engine output record exceeds 4 MiB")
			return 0, w.err
		}
		w.pending = append(w.pending, line...)
		if !found {
			break
		}
		w.flush()
		p = rest
	}
	return n, nil
}

// flush emits the buffered record, withholding malformed JSON and output values.
func (w *eventWriter) flush() {
	line := bytes.TrimSpace(w.pending)
	defer func() { w.pending = nil }()
	if len(line) == 0 {
		return
	}
	if !w.structured {
		w.emit(Event{Type: "log", Message: string(line)})
		return
	}
	var raw struct {
		Type    string `json:"type"`
		Message string `json:"@message"`
		Hook    struct {
			Resource struct {
				Address string `json:"addr"`
			} `json:"resource"`
			Action string `json:"action"`
		} `json:"hook"`
		Diagnostic *struct{ Severity, Summary, Detail, Address string } `json:"diagnostic"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		w.err = fmt.Errorf("invalid engine JSON event (raw record withheld)")
		return
	}
	if raw.Type == "outputs" {
		return
	}
	e := Event{Type: raw.Type, Message: raw.Message, Address: raw.Hook.Resource.Address, Action: raw.Hook.Action}
	if raw.Diagnostic != nil {
		e.Message = raw.Diagnostic.Severity + ": " + raw.Diagnostic.Summary + "\n" + raw.Diagnostic.Detail
		e.Address = raw.Diagnostic.Address
	}
	w.emit(e)
}

// decodePlan builds a redacted review and rejects operations the dashboard cannot explain.
func decodePlan(data []byte) (Plan, error) {
	var raw struct {
		Version   string `json:"format_version"`
		Resources []struct {
			Address         string     `json:"address"`
			PreviousAddress string     `json:"previous_address"`
			Change          planChange `json:"change"`
		} `json:"resource_changes"`
		Outputs  map[string]planChange `json:"output_changes"`
		Deferred []json.RawMessage     `json:"deferred_changes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // Review must not round integer-valued resource attributes.
	if err := decoder.Decode(&raw); err != nil {
		return Plan{}, fmt.Errorf("invalid plan JSON")
	}
	if !strings.HasPrefix(raw.Version, "1.") {
		return Plan{}, fmt.Errorf("unsupported plan format %q", raw.Version)
	}
	if len(raw.Deferred) > 0 {
		return Plan{}, fmt.Errorf("plan contains deferred changes; review it with the engine before applying")
	}
	p := Plan{}
	for _, r := range raw.Resources {
		if r.PreviousAddress != "" || (len(r.Change.Importing) > 0 && string(r.Change.Importing) != "null") {
			return Plan{}, fmt.Errorf("%s includes a move or import; review it with the engine before applying", r.Address)
		}
		if !r.Change.changed() {
			continue
		}
		for _, a := range r.Change.Actions {
			switch a {
			case "create", "update", "delete", "read":
			default:
				return Plan{}, fmt.Errorf("unsupported planned action %q; inspect the plan with the engine", a)
			}
		}
		p.Changes = append(p.Changes, Change{Address: r.Address, Actions: r.Change.Actions, Details: r.Change.details()})
	}
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].Address < p.Changes[j].Address })
	names := make([]string, 0, len(raw.Outputs))
	for name, change := range raw.Outputs {
		if change.changed() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		change := raw.Outputs[name]
		before, _ := json.MarshalIndent(maskPlanValue(change.Before, change.BeforeSensitive, nil), "  ", "  ")
		after, _ := json.MarshalIndent(maskPlanValue(change.After, change.AfterSensitive, change.AfterUnknown), "  ", "  ")
		p.Outputs += name + " · " + strings.Join(change.Actions, " → ") + "\n" +
			"  Before: " + string(before) + "\n  After:  " + string(after) + "\n\n"
	}
	p.HasChanges = len(p.Changes) > 0 || len(names) > 0
	return p, nil
}

// planChange retains engine values and masks until review text has been redacted.
type planChange struct {
	Actions         []string        `json:"actions"`
	Before          any             `json:"before"`
	After           any             `json:"after"`
	BeforeSensitive any             `json:"before_sensitive"`
	AfterSensitive  any             `json:"after_sensitive"`
	AfterUnknown    any             `json:"after_unknown"`
	ReplacePaths    any             `json:"replace_paths"`
	Importing       json.RawMessage `json:"importing"`
}

// changed distinguishes planned work from an unchanged resource or output.
func (c planChange) changed() bool {
	return len(c.Actions) > 0 && (len(c.Actions) != 1 || c.Actions[0] != "no-op")
}

// details renders before and after values with sensitivity and unknown masks applied.
func (c planChange) details() string {
	before, _ := json.MarshalIndent(maskPlanValue(c.Before, c.BeforeSensitive, nil), "", "  ")
	after, _ := json.MarshalIndent(maskPlanValue(c.After, c.AfterSensitive, c.AfterUnknown), "", "  ")
	text := "Operation: " + strings.Join(c.Actions, " → ") + "\n\nBefore\n" + string(before) + "\n\nAfter\n" + string(after)
	if c.ReplacePaths != nil {
		paths, _ := json.Marshal(c.ReplacePaths)
		text += "\n\nReplacement paths: " + string(paths)
	}
	return text
}

// maskPlanValue follows the shape of Terraform's nested sensitivity and unknown
// masks, including attributes omitted from the after value because they are unknown.
func maskPlanValue(value, sensitive, unknown any) any {
	if sensitive == true {
		return "(sensitive)"
	}
	if unknown == true {
		return "(known after apply)"
	}
	sm, _ := sensitive.(map[string]any)
	um, _ := unknown.(map[string]any)
	if object, ok := value.(map[string]any); ok || len(um) > 0 || len(sm) > 0 {
		out := map[string]any{}
		for k, v := range object {
			out[k] = maskPlanValue(v, sm[k], um[k])
		}
		for k := range um {
			if _, ok := out[k]; !ok {
				out[k] = maskPlanValue(nil, sm[k], um[k])
			}
		}
		for k := range sm {
			if _, ok := out[k]; !ok {
				out[k] = maskPlanValue(nil, sm[k], um[k])
			}
		}
		return out
	}
	if array, ok := value.([]any); ok {
		s, _ := sensitive.([]any)
		u, _ := unknown.([]any)
		out := make([]any, len(array))
		for i, v := range array {
			var sv, uv any
			if i < len(s) {
				sv = s[i]
			}
			if i < len(u) {
				uv = u[i]
			}
			out[i] = maskPlanValue(v, sv, uv)
		}
		return out
	}
	return value
}
