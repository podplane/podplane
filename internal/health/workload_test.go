// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDeploymentReplicas(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replicas  string
		wantScale bool
	}{
		{name: "already desired", replicas: "1"},
		{name: "scale from zero", replicas: "0", wantScale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$*" in
  *" get deployment envoy-gateway -o json")
    printf '{"spec":{"replicas":%s}}' "$REPLICAS"
    ;;
  *" scale deployment envoy-gateway --replicas=1")
    ;;
  *)
    printf 'unexpected arguments: %s\n' "$*" >&2
    exit 1
    ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
				t.Fatalf("write fake kubectl: %v", err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("CALLS", calls)
			t.Setenv("REPLICAS", tc.replicas)

			if err := ensureDeploymentReplicas(context.Background(), "", "", "platform-envoy-gateway", "envoy-gateway", 1); err != nil {
				t.Fatalf("ensureDeploymentReplicas: %v", err)
			}
			got, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("read kubectl calls: %v", err)
			}
			scaled := strings.Contains(string(got), "scale deployment envoy-gateway --replicas=1")
			if scaled != tc.wantScale {
				t.Fatalf("scale called = %t, want %t; calls:\n%s", scaled, tc.wantScale, got)
			}
		})
	}
}
