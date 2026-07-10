// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// SelectContainerFunc asks the user to choose one of the available containers.
type SelectContainerFunc func(containers []string) (container string, ok bool, err error)

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
	args := kubectlContextArgs(opts)
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
	info, err := logsPodInfo(opts)
	if err != nil {
		return "", err
	}
	return selectDefaultLogsContainer(info, opts)
}

// selectDefaultLogsContainer chooses a default from discovered container metadata.
func selectDefaultLogsContainer(info logsContainerInfo, opts LogsOptions) (string, error) {
	if info.DefaultContainer != "" {
		return info.DefaultContainer, nil
	}
	if info.AppContainer != "" {
		return info.AppContainer, nil
	}
	if len(info.Containers) == 1 {
		return info.Containers[0], nil
	}
	if len(info.Containers) == 0 {
		return "", fmt.Errorf("no pods found for app %q", opts.Name)
	}
	if opts.SelectContainer != nil {
		container, ok, err := opts.SelectContainer(info.Containers)
		if err != nil {
			return "", err
		}
		if ok && container != "" {
			return container, nil
		}
		return "", fmt.Errorf("container selection cancelled")
	}
	return "", fmt.Errorf("multiple containers found for app %q; choose one with `podplane logs %s --container <%s>` or stream all containers with `podplane logs %s --all`", opts.Name, opts.Name, strings.Join(info.Containers, "|"), opts.Name)
}

// logsContainerInfo describes the containers available for an app's pods.
type logsContainerInfo struct {
	Containers       []string
	DefaultContainer string
	AppContainer     string
}

// logsPodInfo reads pod container metadata for the requested app.
func logsPodInfo(opts LogsOptions) (logsContainerInfo, error) {
	args := kubectlContextArgs(opts)
	args = append(args, "get", "pods", "-l", appSelector(opts.Name), "-o", "json")
	cmd := execwrap.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return logsContainerInfo{}, fmt.Errorf("list app pods: %w", err)
	}
	return parseLogsPodInfo(opts.Name, out)
}

// parseLogsPodInfo extracts log container defaults from kubectl pod JSON.
func parseLogsPodInfo(appName string, raw []byte) (logsContainerInfo, error) {
	var pods struct {
		Items []struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &pods); err != nil {
		return logsContainerInfo{}, fmt.Errorf("parse app pods: %w", err)
	}

	seen := map[string]bool{}
	defaultContainer := ""
	appContainer := ""
	preferredAppContainers := map[string]bool{
		appName + "-container": true,
		"web":                  true,
		"app":                  true,
	}
	for _, pod := range pods.Items {
		podContainers := map[string]bool{}
		for _, container := range pod.Spec.Containers {
			if container.Name == "" {
				continue
			}
			seen[container.Name] = true
			podContainers[container.Name] = true
			if preferredAppContainers[container.Name] {
				appContainer = container.Name
			}
		}
		if name := pod.Metadata.Annotations["kubectl.kubernetes.io/default-container"]; name != "" && podContainers[name] {
			defaultContainer = name
		}
	}

	containers := make([]string, 0, len(seen))
	for name := range seen {
		containers = append(containers, name)
	}
	sort.Strings(containers)
	return logsContainerInfo{Containers: containers, DefaultContainer: defaultContainer, AppContainer: appContainer}, nil
}

// kubectlContextArgs returns kubectl context flags common to deploy commands.
func kubectlContextArgs(opts LogsOptions) []string {
	args := []string{}
	if opts.Context != "" {
		args = append(args, "--context", opts.Context)
	}
	if opts.Kubeconfig != "" {
		args = append(args, "--kubeconfig", opts.Kubeconfig)
	}
	if opts.Namespace != "" {
		args = append(args, "--namespace", opts.Namespace)
	}
	return args
}

// appSelector returns the Kubernetes label selector for an app release.
func appSelector(name string) string {
	return "app.kubernetes.io/instance=" + name
}
