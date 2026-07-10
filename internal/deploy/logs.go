// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"os"

	"github.com/podplane/podplane/internal/execwrap"
)

// LogsOptions controls log streaming for a deployed app.
type LogsOptions struct {
	Name            string
	Namespace       string
	Context         string
	Kubeconfig      string
	Container       string
	AllContainers   bool
	SelectContainer SelectContainerFunc
}

// Logs invokes `kubectl logs --follow` for pods belonging to the named app.
func Logs(opts LogsOptions) error {
	if !opts.AllContainers && opts.Container == "" {
		container, err := defaultLogsContainer(opts)
		if err != nil {
			return err
		}
		opts.Container = container
	}

	cmd := execwrap.Command("kubectl", logsArgs(opts)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// logsArgs builds the kubectl logs command for the requested app.
func logsArgs(opts LogsOptions) []string {
	args := kubectlContextArgs(logsAppTarget(opts))
	args = append(args, "logs", "--follow")
	if opts.AllContainers {
		args = append(args, "--all-containers=true", "--prefix=true")
	} else if opts.Container != "" {
		args = append(args, "--container", opts.Container)
	}
	args = append(args, "-l", appSelector(opts.Name))
	return args
}

// defaultLogsContainer returns the container to use when the user did not choose one.
func defaultLogsContainer(opts LogsOptions) (string, error) {
	info, err := readAppInfo(logsAppTarget(opts))
	if err != nil {
		return "", err
	}
	return selectDefaultAppContainer(info, logsAppTarget(opts), "logs", true)
}

// logsAppTarget adapts log options to the shared app target options.
func logsAppTarget(opts LogsOptions) AppTargetOptions {
	return AppTargetOptions{
		Name:            opts.Name,
		Namespace:       opts.Namespace,
		Context:         opts.Context,
		Kubeconfig:      opts.Kubeconfig,
		Container:       opts.Container,
		SelectContainer: opts.SelectContainer,
	}
}
