// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanAndApplySavedPlan exercises approval, opaque plan reuse, redaction, event
// streaming and cleanup through the executor's public plan-and-apply operation.
func TestPlanAndApplySavedPlan(t *testing.T) {
	for _, scenario := range []string{"approve", "cancel", "malformed", "failure", "nochange", "outputs"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			script := `#!/bin/sh
case "$1" in
init) echo initialized ;;
plan)
  for arg in "$@"; do case "$arg" in -out=*) path=${arg#-out=}; printf '%s' "$path" > plan-path; printf opaque > "$path";; esac; done
  if [ "$SCENARIO" = malformed ]; then echo invalid-json; exit 2; fi
  printf '%s' '{"type":"version","@message":"engine"}'
  printf '\n'
  exit 2 ;;
show)
  if [ "$SCENARIO" = nochange ]; then echo '{"format_version":"1.2"}'; exit; fi
  if [ "$SCENARIO" = outputs ]; then echo '{"format_version":"1.2","output_changes":{"name":{"actions":["create"],"after":"hello"},"credentials":{"actions":["update"],"before":"old-output-secret","before_sensitive":true,"after":{"token":"new-output-secret"},"after_sensitive":{"token":true},"after_unknown":{"endpoint":true}}}}'; exit; fi
  cat <<'JSON'
{"format_version":"1.2","resource_changes":[{"address":"terraform_data.example","change":{"actions":["delete","create"],"before":{"password":"old-secret"},"before_sensitive":{"password":true},"after":{"nested":[{"token":"new-secret","visible":9007199254740993}]},"after_sensitive":{"nested":[{"token":true}]},"after_unknown":{"id":true}}}]}
JSON
  ;;
apply)
  for arg in "$@"; do path=$arg; done
  test "$path" = "$(cat plan-path)" || exit 15
  test "$(cat "$path")" = opaque || exit 16
  touch applied
  echo '{"type":"apply_start","hook":{"resource":{"addr":"terraform_data.example"},"action":"create"}}'
  echo '{"type":"future_event","@message":"future message"}'
  if [ "$SCENARIO" = failure ]; then echo '{"type":"diagnostic","diagnostic":{"severity":"error","summary":"Saved plan is stale","detail":"Replan first"}}'; exit 1; fi
  echo '{"type":"apply_complete","hook":{"resource":{"addr":"terraform_data.example"},"action":"create"}}'
  echo '{"type":"outputs","@message":"do not expose outputs","outputs":{"secret":"hidden"}}'
  ;;
esac
`
			bin := filepath.Join(dir, "engine")
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SCENARIO", scenario)
			cli := &CLI{binary: bin}
			var events []Event
			reviewed := false
			err := cli.PlanAndApply(context.Background(), dir, "backend", func(e Event) { events = append(events, e) }, func(p Plan) (bool, error) {
				reviewed = true
				path, err := os.ReadFile(filepath.Join(dir, "plan-path"))
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(filepath.Dir(string(path)))
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatalf("private plan directory: %v, %v", info, err)
				}
				if scenario == "nochange" {
					if p.HasChanges {
						t.Fatal("no-change plan needs no apply")
					}
					return true, nil
				}
				if scenario == "outputs" {
					if !p.HasChanges || !strings.Contains(p.Outputs, "hello") {
						t.Fatalf("output-only plan: %+v", p)
					}
					for _, secret := range []string{"old-output-secret", "new-output-secret"} {
						if strings.Contains(p.Outputs, secret) {
							t.Fatal("output secret exposed")
						}
					}
					for _, visible := range []string{"(sensitive)", "(known after apply)", "credentials", "name"} {
						if !strings.Contains(p.Outputs, visible) {
							t.Fatalf("missing output value %q", visible)
						}
					}
					return true, nil
				}
				if len(p.Changes) != 1 || p.Changes[0].Action() != "Replace" {
					t.Fatalf("plan: %+v", p)
				}
				details := p.Changes[0].Details
				for _, secret := range []string{"old-secret", "new-secret"} {
					if strings.Contains(details, secret) {
						t.Fatal("secret exposed")
					}
				}
				for _, visible := range []string{"9007199254740993", "(sensitive)", "(known after apply)"} {
					if !strings.Contains(details, visible) {
						t.Fatalf("missing %q: %s", visible, details)
					}
				}
				return scenario != "cancel", nil
			})
			wantError := scenario == "cancel" || scenario == "malformed" || scenario == "failure"
			if (err != nil) != wantError {
				t.Fatalf("PlanAndApply error: %v", err)
			}
			if reviewed != (scenario != "malformed") {
				t.Fatalf("reviewed=%v", reviewed)
			}
			_, appliedErr := os.Stat(filepath.Join(dir, "applied"))
			wantApply := scenario == "approve" || scenario == "failure" || scenario == "outputs"
			if (appliedErr == nil) != wantApply {
				t.Fatalf("applied=%v", appliedErr == nil)
			}
			path, _ := os.ReadFile(filepath.Join(dir, "plan-path"))
			if _, err := os.Stat(filepath.Dir(string(path))); !os.IsNotExist(err) {
				t.Fatalf("plan directory not cleaned: %v", err)
			}
			if wantApply {
				found := false
				for _, e := range events {
					if e.Type == "future_event" {
						found = true
					}
					if e.Type == "outputs" {
						t.Fatal("raw outputs exposed")
					}
				}
				if !found {
					t.Fatal("unknown event message was discarded")
				}
			}
		})
	}
}
