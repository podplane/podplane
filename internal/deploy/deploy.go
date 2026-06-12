// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import "time"

// Options controls a single app deployment.
type Options struct {
	Template   string
	Name       string
	ChartPath  string
	Image      string
	Env        []string
	Hostname   string
	Path       string
	Set        []string
	Namespace  string
	Context    string
	Kubeconfig string
	Wait       bool
	Timeout    time.Duration
}

// Run renders Helm values from opts and invokes `helm upgrade --install` for
// the resolved chart. The caller is responsible for ensuring opts.ChartPath
// exists (typically via EnsureChart).
func Run(opts Options) error {
	env, err := parseEnv(opts.Env)
	if err != nil {
		return err
	}
	if err := validateTemplateValuesSchema(opts.Template, opts.ChartPath, opts.Hostname != "", opts.Path != ""); err != nil {
		return err
	}
	return withValuesFile(opts.Image, env, opts.Hostname, opts.Path, func(valuesPath string) error {
		return runHelm(helmUpgradeInstallArgs(opts, valuesPath))
	})
}

// helmUpgradeInstallArgs builds the Helm upgrade/install command for an app
// deployment. When opts.Wait is true, Helm waits for Kubernetes to report the
// rendered resources ready before printing chart notes.
func helmUpgradeInstallArgs(opts Options, valuesPath string) []string {
	args := []string{"upgrade", "--install", opts.Name, opts.ChartPath, "-f", valuesPath}
	if opts.Wait {
		args = append(args, "--wait", "--timeout", opts.Timeout.String())
	}
	for _, value := range opts.Set {
		args = append(args, "--set", value)
	}
	if opts.Namespace != "" {
		args = append(args, "--namespace", opts.Namespace, "--create-namespace")
	}
	if opts.Context != "" {
		args = append(args, "--kube-context", opts.Context)
	}
	if opts.Kubeconfig != "" {
		args = append(args, "--kubeconfig", opts.Kubeconfig)
	}
	return args
}
