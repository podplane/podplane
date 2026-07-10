// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package oidccreate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/podplane/podplane/internal/oidcconfig"
	"github.com/podplane/podplane/internal/tfexec"
	"github.com/podplane/podplane/internal/tfgen"
	"github.com/podplane/podplane/internal/tui"
)

// Options controls the OIDC create workflow.
type Options struct {
	ConfigPath  string
	NoApply     bool
	AutoApprove bool
}

// Run creates or loads an OIDC config, writes generated Terraform, and
// optionally applies it.
func Run(ctx context.Context, opts Options) (string, error) {
	var cfg *oidcconfig.Config
	if _, err := os.Stat(opts.ConfigPath); os.IsNotExist(err) {
		fmt.Println("Podplane will deploy Easy OIDC.")
		fmt.Println("Most organisations use one OIDC server across production, staging, development, observability, and CI/CD clusters.")
		fmt.Println("Consider deploying it in a dedicated or production-grade account, not casually inside the cluster account you happen to be creating today.")
		cfg, err = RunConfigWizard()
		if err != nil {
			return "", err
		}
		if err := oidcconfig.Write(opts.ConfigPath, cfg); err != nil {
			return "", err
		}
		fmt.Printf("Created %s\n", opts.ConfigPath)
	} else if err != nil {
		return "", err
	} else {
		var err error
		cfg, err = oidcconfig.Load(opts.ConfigPath)
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Dir(opts.ConfigPath)
	if err := tfgen.WriteOIDC(dir, cfg); err != nil {
		return "", err
	}
	fmt.Printf("Generated Podplane OpenTofu/Terraform files in %s\n", dir)
	if opts.NoApply {
		return cfg.IssuerURL(), nil
	}
	executor, err := tfexec.NewCLI()
	if err != nil {
		return "", err
	}
	applyCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	if err := executor.Init(applyCtx, dir); err != nil {
		return "", err
	}
	ok, err := tui.Confirm("Apply generated OpenTofu/Terraform changes?", opts.AutoApprove, 0)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("apply cancelled")
	}
	executorErr := executor.Apply(applyCtx, dir, opts.AutoApprove)
	if cfg.OIDC.Domain.Provider.Kind != "aws" || cfg.OIDC.Domain.Zone == "" {
		fmt.Println("Manual DNS configuration may be required for the generated OIDC endpoint.")
	}
	return cfg.IssuerURL(), executorErr
}
