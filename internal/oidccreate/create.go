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
	"github.com/podplane/podplane/internal/tfstate"
	"github.com/podplane/podplane/internal/tui"
)

// Options controls the OIDC create workflow.
type Options struct {
	// ConfigPath is the OIDC config file to load, or to create with the
	// config wizard when it does not exist.
	ConfigPath string
	// Provider supplies the cluster's cloud settings for a new config, skipping
	// duplicate provider questions. Nil lets the standalone wizard collect them.
	// Existing OIDC configs keep their own provider settings.
	Provider *oidcconfig.Provider
	// ClientID is the OIDC client ID of the cluster being created alongside
	// the server, if any. A new config registers a client for it; when an
	// existing config has none, Run warns that the cluster cannot log in.
	ClientID string
	// NoApply writes the generated files without contacting the cloud.
	NoApply bool
	// AutoApprove skips confirmation prompts.
	AutoApprove bool
}

// Run loads or creates an OIDC config, writes its generated
// OpenTofu/Terraform stack and remote-state backend config beside it, and
// unless opts.NoApply is set, applies the stack. It returns the server's
// issuer URL. podplane oidc create and podplane cluster create both use it.
func Run(ctx context.Context, opts Options) (string, error) {
	cfg, err := loadOrCreate(opts)
	if err != nil {
		return "", err
	}
	backend, err := tfstate.ForOIDC(cfg)
	if err != nil {
		return "", err
	}
	if !opts.NoApply {
		if err := tui.RunVerification(ctx, "Checking cloud access", "Remote-state bucket: "+backend.Bucket, backend.Preflight, opts.AutoApprove); err != nil {
			return "", err
		}
	}
	dir := filepath.Dir(opts.ConfigPath)
	if err := tfgen.WriteOIDC(dir, cfg); err != nil {
		return "", err
	}
	if err := backend.WriteConfig(dir); err != nil {
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
	confirm := func(question string) (bool, error) { return tui.Confirm(question, opts.AutoApprove, 0) }
	if err := backend.Ensure(ctx, os.Stderr, confirm); err != nil {
		return "", err
	}
	ok, err := confirm("Apply generated OpenTofu/Terraform changes?")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("apply cancelled")
	}
	applyCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	if err := executor.InitApply(applyCtx, dir, backend.ConfigFile(), opts.AutoApprove); err != nil {
		return "", err
	}
	if cfg.OIDC.Domain.Provider.Kind == "" || cfg.OIDC.Domain.Zone == "" {
		fmt.Println("Manual DNS configuration may be required for the generated OIDC endpoint.")
	}
	return cfg.IssuerURL(), nil
}

// loadOrCreate loads the OIDC config at opts.ConfigPath, or creates it with
// the config wizard when it does not exist.
func loadOrCreate(opts Options) (*oidcconfig.Config, error) {
	if _, err := os.Stat(opts.ConfigPath); err == nil {
		cfg, err := oidcconfig.Load(opts.ConfigPath)
		if err != nil {
			return nil, err
		}
		if opts.ClientID != "" && !cfg.HasClient(opts.ClientID) {
			fmt.Printf("Warning: %s has no Truster client %q. Add it under oidc.truster.static_policy.clients and run podplane oidc create again so the cluster can log in.\n", opts.ConfigPath, opts.ClientID)
		}
		return cfg, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	fmt.Println("Podplane will deploy Truster.")
	fmt.Println("Most organisations use one OIDC server across production, staging, development, observability, and CI/CD clusters.")
	if opts.Provider != nil {
		fmt.Println("Reusing the cluster's cloud settings for this Truster server.")
	} else {
		fmt.Println("Consider deploying it in a dedicated or production-grade account, not casually inside the cluster account you happen to be creating today.")
	}
	cfg, err := RunConfigWizard(opts.Provider, opts.ClientID)
	if err != nil {
		return nil, err
	}
	if err := oidcconfig.Write(opts.ConfigPath, cfg); err != nil {
		return nil, err
	}
	fmt.Printf("Created %s\n", opts.ConfigPath)
	return cfg, nil
}
