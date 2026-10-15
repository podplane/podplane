// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clustersleep

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const sleepContexts = `{"current-context":"alias","contexts":[{"name":"demo","context":{"cluster":"podplane-demo"}},{"name":"alias","context":{"cluster":"podplane-demo"}},{"name":"wrong","context":{"cluster":"other"}},{"name":"local","context":{"cluster":"podplane-local-demo"}}]}`
const sleepCluster = `{"metadata":{"name":"demo--tenant","namespace":"operator-ns","annotations":{"nstance.dev/sleep-progress":"checkpoint","example.com/keep":"keep"}},"spec":{"infrastructureRef":{"kind":"NstanceCluster","name":"demo--tenant"}}}`

// fakeSleepKubectl installs a shell fake that records sleep requests and serves configurable responses.
func fakeSleepKubectl(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake")
	}
	dir := t.TempDir()
	t.Setenv("SLEEP_LOG", filepath.Join(dir, "calls"))
	t.Setenv("SLEEP_REVIEW", filepath.Join(dir, "review"))
	t.Setenv("SLEEP_PATCH", filepath.Join(dir, "patch"))
	t.Setenv("SLEEP_CONTEXTS", sleepContexts)
	t.Setenv("SLEEP_CLUSTERS", `{"items":[`+sleepCluster+`]}`)
	t.Setenv("SLEEP_ACCESS", `{"status":{"allowed":true}}`)
	t.Setenv("SLEEP_FAIL", "")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SLEEP_LOG"
while [ "$1" = "--context" ] || [ "$1" = "--kubeconfig" ]; do shift 2; done
if [ "$1" = "$SLEEP_FAIL" ]; then echo 'server unavailable' >&2; exit 1; fi
case "$1" in
config) printf '%s' "$SLEEP_CONTEXTS" ;;
get) printf '%s' "$SLEEP_CLUSTERS" ;;
create) cat > "$SLEEP_REVIEW"; printf '%s' "$SLEEP_ACCESS" ;;
patch)
  while [ "$1" != "--patch" ] && [ "$#" -gt 0 ]; do shift; done
  shift; printf '%s' "$1" > "$SLEEP_PATCH"
  ;;
*) echo 'unexpected call (polling is forbidden)' >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// sleepRead reads a recorded kubectl call, access review, or patch from the fixture directory.
func sleepRead(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRequestSleepAnnotationAndImmediateReturn checks authorization, timestamped merge patches, and absence of polling.
func TestRequestSleepAnnotationAndImmediateReturn(t *testing.T) {
	for _, mode := range []string{"normal", "confirmed", "auto-approved"} {
		t.Run(mode, func(t *testing.T) {
			dir := fakeSleepKubectl(t)
			warned, confirmed := false, false
			opts := Options{Context: "demo", Kubeconfig: "/custom/kubeconfig", Force: mode != "normal", AutoApprove: mode == "auto-approved",
				Warn:    func(s string) { warned = strings.Contains(s, "active workloads and connections") },
				Confirm: func(s string) (bool, error) { confirmed = true; return true, nil },
			}
			before := time.Now().UTC().Truncate(time.Second)
			if err := Request(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			if warned != opts.Force || confirmed != (mode == "confirmed") {
				t.Fatalf("warning=%v confirmation=%v", warned, confirmed)
			}
			calls := strings.Split(strings.TrimSpace(sleepRead(t, dir, "calls")), "\n")
			if len(calls) != 4 {
				t.Fatalf("expected context/discovery/review/patch only, got %v", calls)
			}
			for _, call := range calls {
				if !strings.HasPrefix(call, "--context demo --kubeconfig /custom/kubeconfig ") {
					t.Fatalf("wrong selection: %s", call)
				}
			}
			if !strings.Contains(calls[2], "create --filename=-") || !strings.Contains(calls[3], "patch clusters.cluster.x-k8s.io demo--tenant --namespace operator-ns --type=merge") {
				t.Fatalf("wrong order or patch target: %v", calls)
			}
			var review struct {
				Kind string
				Spec struct{ ResourceAttributes map[string]string }
			}
			if err := json.Unmarshal([]byte(sleepRead(t, dir, "review")), &review); err != nil {
				t.Fatal(err)
			}
			attrs := review.Spec.ResourceAttributes
			if review.Kind != "SelfSubjectAccessReview" || attrs["group"] != "cluster.x-k8s.io" || attrs["resource"] != "clusters" || attrs["verb"] != "patch" || attrs["name"] != "demo--tenant" || attrs["namespace"] != "operator-ns" {
				t.Fatalf("incorrect access review: %+v", review)
			}
			var patch struct {
				Metadata struct{ Annotations map[string]string }
			}
			if err := json.Unmarshal([]byte(sleepRead(t, dir, "patch")), &patch); err != nil {
				t.Fatal(err)
			}
			if len(patch.Metadata.Annotations) != 1 {
				t.Fatalf("patch overwrites other annotations: %+v", patch)
			}
			value := patch.Metadata.Annotations["nstance.dev/sleep-request"]
			if strings.HasPrefix(value, "force:") != opts.Force {
				t.Fatalf("incorrect force annotation: %s", value)
			}
			stamp, err := time.Parse(time.RFC3339, strings.TrimPrefix(value, "force:"))
			if err != nil || !strings.HasSuffix(value, "Z") || stamp.Before(before) || stamp.After(time.Now()) {
				t.Fatalf("invalid UTC timestamp %q: %v", value, err)
			}
			// Apply the map merge to the existing annotations: the only provided
			// key must leave the durable progress checkpoint and user keys intact.
			annotations := map[string]string{"nstance.dev/sleep-progress": "checkpoint", "example.com/keep": "keep"}
			for k, v := range patch.Metadata.Annotations {
				annotations[k] = v
			}
			if annotations["nstance.dev/sleep-progress"] != "checkpoint" || annotations["example.com/keep"] != "keep" {
				t.Fatal("annotations were lost")
			}
		})
	}
}

// TestRequestSleepRefusesUnsafeTargetsAndAccess checks target selection and access failures prevent mutation.
func TestRequestSleepRefusesUnsafeTargetsAndAccess(t *testing.T) {
	for _, tc := range []struct {
		name, env, value, selected, want string
		calls                            int
	}{
		{"denied", "SLEEP_ACCESS", `{"status":{"allowed":false,"reason":"RBAC denied"}}`, "", "RBAC denied", 3},
		{"missing status", "SLEEP_ACCESS", `{}`, "", "not authorized", 3},
		{"evaluation error", "SLEEP_ACCESS", `{"status":{"allowed":true,"evaluationError":"failed"}}`, "", "not authorized", 3},
		{"invalid review", "SLEEP_ACCESS", `bad`, "", "parse patch access review", 3},
		{"mismatch", "", "", "wrong", "does not reference", 1},
		{"local", "", "", "local", "does not reference", 1},
		{"missing context", "", "", "missing", "not found", 1},
		{"no target", "SLEEP_CLUSTERS", `{"items":[]}`, "", "found 0", 2},
		{"ambiguous", "SLEEP_CLUSTERS", `{"items":[` + sleepCluster + `,` + sleepCluster + `]}`, "", "found 2", 2},
		{"discovery failure", "SLEEP_FAIL", "get", "", "discover Nstance CAPI Cluster", 2},
		{"review failure", "SLEEP_FAIL", "create", "", "review CAPI Cluster patch access", 3},
		{"patch failure", "SLEEP_FAIL", "patch", "", "request sleep of CAPI Cluster", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fakeSleepKubectl(t)
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			err := Request(context.Background(), Options{Context: tc.selected})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			if got := len(strings.Split(strings.TrimSpace(sleepRead(t, dir, "calls")), "\n")); got != tc.calls {
				t.Fatalf("got %d calls, want %d", got, tc.calls)
			}
			if _, err := os.Stat(filepath.Join(dir, "patch")); !os.IsNotExist(err) {
				t.Fatal("unsafe patch")
			}
		})
	}
}

// TestRequestSleepCurrentContextAndCancellation checks context pinning and propagation of cancellation.
func TestRequestSleepCurrentContextAndCancellation(t *testing.T) {
	dir := fakeSleepKubectl(t)
	if err := Request(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sleepRead(t, dir, "calls"), "--context alias patch clusters.cluster.x-k8s.io demo--tenant ") {
		t.Fatal("current context not selected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Request(ctx, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// TestRequestSleepForceNeedsConfirmation checks missing, declined, or failed confirmation prevents forced sleep.
func TestRequestSleepForceNeedsConfirmation(t *testing.T) {
	for _, mode := range []string{"missing", "declined", "error"} {
		t.Run(mode, func(t *testing.T) {
			dir := fakeSleepKubectl(t)
			opts := Options{Force: true}
			if mode != "missing" {
				opts.Confirm = func(string) (bool, error) {
					if mode == "error" {
						return false, errors.New("prompt failed")
					}
					return false, nil
				}
			}
			if err := Request(context.Background(), opts); err == nil {
				t.Fatal("expected refusal")
			}
			if got := len(strings.Split(strings.TrimSpace(sleepRead(t, dir, "calls")), "\n")); got != 2 {
				t.Fatalf("mutation after failed confirmation: %d calls", got)
			}
		})
	}
}
