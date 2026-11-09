// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tui"
)

//go:embed main.tf
var fixture string

// main reports demo failures through the process exit status.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run exercises the plan-and-apply dashboard with disposable local resources.
func run() error {
	fail := flag.Bool("fail", false, "Fail one local resource during apply")
	autoApprove := flag.Bool("auto-approve", false, "Approve without prompting (also works without a terminal)")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flag.Args())
	}
	executor, err := tfexec.NewCLI()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "podplane-demo-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for name, data := range map[string]string{
		"main.tf":          fixture,
		"backend.hcl":      "path = \"terraform.tfstate\"\n",
		"terraform.tfvars": fmt.Sprintf("fail = %t\n", *fail),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			return err
		}
	}
	return tui.RunPlanAndApply(context.Background(), tui.PlanAndApplyOptions{
		Title:    "Podplane · Plan-and-apply demo",
		Subtitle: "Local terraform_data resources only · temporary state is deleted on exit",
		Resume:   "make demo-cluster-create", AutoApprove: *autoApprove,
	}, func(ctx context.Context, emit func(tfexec.Event), review func(tfexec.Plan) (bool, error)) error {
		return executor.PlanAndApply(ctx, dir, "backend.hcl", emit, review)
	})
}
