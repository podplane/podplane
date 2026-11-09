// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestStagedDestroyEngines checks dependency expansion and cleanup safety with both engines.
func TestStagedDestroyEngines(t *testing.T) {
	for _, engine := range []string{"tofu", "terraform"} {
		t.Run(engine, func(t *testing.T) {
			binary, err := exec.LookPath(engine)
			if err != nil {
				t.Skip("engine not installed")
			}
			dir := t.TempDir()
			configuration := `resource "terraform_data" "name" { input = "bucket-name" }
resource "terraform_data" "bucket" { input = terraform_data.name.output }
resource "terraform_data" "server" { input = terraform_data.bucket.output }
resource "terraform_data" "independent" { input = "network" }
`
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cli := &CLI{binary: binary}
			for _, args := range [][]string{{"init", "-input=false"}, {"apply", "-auto-approve", "-input=false"}} {
				if err := cli.run(ctx, dir, args...); err != nil {
					t.Fatal(err)
				}
			}
			if err := cli.CheckDefaultWorkspace(ctx, dir); err != nil {
				t.Fatal(err)
			}
			state, err := cli.ReadState(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			// An incomplete dependency inventory must fail closed, not delete storage.
			bad := state
			bad.Resources = slices.Clone(state.Resources)
			for i := range bad.Resources {
				bad.Resources[i].Dependencies = nil
			}
			dataPath := filepath.Join(dir, "bucket-data")
			if err := os.WriteFile(dataPath, []byte("valuable data"), 0600); err != nil {
				t.Fatal(err)
			}
			emptyBucket := func(context.Context) error { return os.Remove(dataPath) }
			if err := cli.DestroyExcept(ctx, dir, bad, map[string]bool{"terraform_data.bucket": true}, false, emptyBucket); err == nil {
				t.Fatal("unsafe expanded plan was accepted")
			}
			if _, err := os.Stat(dataPath); err != nil {
				t.Fatal("unsafe plan destroyed data")
			}
			if err := cli.DestroyExcept(ctx, dir, state, map[string]bool{"terraform_data.bucket": true}, false, nil); err != nil {
				t.Fatal(err)
			}
			remaining, err := cli.ReadState(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			addresses := []string{}
			for _, r := range remaining.Resources {
				addresses = append(addresses, r.Address)
			}
			slices.Sort(addresses)
			if !slices.Equal(addresses, []string{"terraform_data.bucket", "terraform_data.name"}) {
				t.Fatalf("retained resources: %v", addresses)
			}
			// Repeating the first phase must not accidentally become a full destroy.
			if err := cli.DestroyExcept(ctx, dir, remaining, map[string]bool{"terraform_data.bucket": true}, false, nil); err != nil {
				t.Fatal(err)
			}
			protected := strings.Replace(configuration, `resource "terraform_data" "bucket" { input = terraform_data.name.output }`, "resource \"terraform_data\" \"bucket\" {\n  input = terraform_data.name.output\n  lifecycle { prevent_destroy = true }\n}", 1)
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(protected), 0600); err != nil {
				t.Fatal(err)
			}
			if err := cli.DestroyExcept(ctx, dir, remaining, map[string]bool{}, false, emptyBucket); err == nil {
				t.Fatal("protected resource was destroyed")
			}
			if data, err := os.ReadFile(dataPath); err != nil || string(data) != "valuable data" {
				t.Fatal("rejected destroy plan lost bucket contents")
			}
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			blocked := errors.New("object retention prevents emptying")
			if err := cli.DestroyExcept(ctx, dir, remaining, map[string]bool{}, false, func(context.Context) error { return blocked }); !errors.Is(err, blocked) {
				t.Fatalf("cleanup failure = %v", err)
			}
			retained, err := cli.ReadState(ctx, dir)
			if err != nil || len(retained.Resources) != 2 {
				t.Fatal("cleanup failure did not preserve managed storage")
			}
			if err := cli.DestroyExcept(ctx, dir, remaining, map[string]bool{}, false, emptyBucket); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
				t.Fatal("approved cleanup did not empty bucket")
			}
			remaining, err = cli.ReadState(ctx, dir)
			if err != nil || len(remaining.Resources) != 0 {
				t.Fatalf("final state = %v, %v", remaining, err)
			}
			if err := cli.run(ctx, dir, "workspace", "new", "other"); err != nil {
				t.Fatal(err)
			}
			if err := cli.CheckDefaultWorkspace(ctx, dir); err == nil {
				t.Fatal("accepted a workspace whose state would not be locked")
			}
		})
	}
}
