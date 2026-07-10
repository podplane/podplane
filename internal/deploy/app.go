// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// SelectContainerFunc asks the user to choose one of the available containers.
type SelectContainerFunc func(containers []string) (container string, ok bool, err error)

// AppTargetOptions identifies a deployed app in Kubernetes.
type AppTargetOptions struct {
	Name            string
	Namespace       string
	Context         string
	Kubeconfig      string
	Container       string
	SelectContainer SelectContainerFunc
}

// appInfo describes pods and containers for a deployed app.
type appInfo struct {
	Pods             []appPodInfo
	Containers       []string
	DefaultContainer string
	AppContainer     string
}

// appPodInfo describes one pod candidate for app commands.
type appPodInfo struct {
	Name  string
	Ready bool
}

// appTarget resolves the pod and container to use for an app command.
func appTarget(opts AppTargetOptions, command string) (string, string, error) {
	info, err := readAppInfo(opts)
	if err != nil {
		return "", "", err
	}
	pod, err := selectDefaultAppPod(info, opts)
	if err != nil {
		return "", "", err
	}
	container := opts.Container
	if container == "" {
		container, err = selectDefaultAppContainer(info, opts, command, false)
		if err != nil {
			return "", "", err
		}
	}
	return pod, container, nil
}

// selectDefaultAppPod chooses a Ready pod for app commands that need one pod.
func selectDefaultAppPod(info appInfo, opts AppTargetOptions) (string, error) {
	if len(info.Pods) == 0 {
		return "", fmt.Errorf("no pods found for app %q", opts.Name)
	}
	for _, pod := range info.Pods {
		if pod.Ready {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("app %q has pods, but none are Ready yet", opts.Name)
}

// selectDefaultAppContainer chooses a default from discovered container metadata.
func selectDefaultAppContainer(info appInfo, opts AppTargetOptions, command string, suggestAll bool) (string, error) {
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
	msg := fmt.Sprintf("multiple containers found for app %q; choose one with `podplane %s %s --container <%s>`", opts.Name, command, opts.Name, strings.Join(info.Containers, "|"))
	if suggestAll {
		msg += fmt.Sprintf(" or stream all containers with `podplane %s %s --all`", command, opts.Name)
	}
	return "", fmt.Errorf("%s", msg)
}

// readAppInfo reads pod container metadata for the requested app.
func readAppInfo(opts AppTargetOptions) (appInfo, error) {
	args := kubectlContextArgs(opts)
	args = append(args, "get", "pods", "-l", appSelector(opts.Name), "-o", "json")
	cmd := execwrap.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return appInfo{}, fmt.Errorf("list app pods: %w", err)
	}
	return parseAppInfo(opts.Name, out)
}

// parseAppInfo extracts pod and container defaults from kubectl pod JSON.
func parseAppInfo(appName string, raw []byte) (appInfo, error) {
	var pods struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &pods); err != nil {
		return appInfo{}, fmt.Errorf("parse app pods: %w", err)
	}

	seen := map[string]bool{}
	defaultContainer := ""
	appContainer := ""
	preferredAppContainers := map[string]bool{
		appName + "-container": true,
		"web":                  true,
		"app":                  true,
	}
	appPods := make([]appPodInfo, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.Metadata.Name != "" {
			appPods = append(appPods, appPodInfo{Name: pod.Metadata.Name, Ready: podReady(pod.Status.Conditions)})
		}
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
	sort.Slice(appPods, func(i, j int) bool { return appPods[i].Name < appPods[j].Name })
	return appInfo{Pods: appPods, Containers: containers, DefaultContainer: defaultContainer, AppContainer: appContainer}, nil
}

// podReady reports whether pod conditions include Ready=True.
func podReady(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) bool {
	for _, condition := range conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return true
		}
	}
	return false
}

// kubectlContextArgs returns kubectl context flags common to deploy commands.
func kubectlContextArgs(opts AppTargetOptions) []string {
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
