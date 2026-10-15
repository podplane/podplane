// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
)

// podSecurityProblems explains why an app's Pods were rejected or could not
// start because of Pod Security settings. It returns nil when it finds none
// or cannot read the app's state.
func podSecurityProblems(target AppTargetOptions) []string {
	args := kubectlContextArgs(target)
	args = append(args, "get", "deployments,pods", "-l", appSelector(target.Name), "-o", "json")
	out, err := execwrap.Command("kubectl", args...).Output()
	if err != nil {
		return nil
	}
	return podSecurityProblemsFromList(out)
}

// podSecurityProblemsFromList extracts Pod Security problems from a kubectl
// List of the app's Deployments and Pods.
func podSecurityProblemsFromList(data []byte) []string {
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Message string `json:"message"`
				} `json:"conditions"`
				InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
				ContainerStatuses     []containerStatus `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil
	}
	var problems []string
	seen := map[string]bool{}
	add := func(problem string) {
		if !seen[problem] {
			seen[problem] = true
			problems = append(problems, problem)
		}
	}
	for _, item := range list.Items {
		switch item.Kind {
		case "Deployment":
			// The ReplicaSet controller reports admission rejections here.
			for _, condition := range item.Status.Conditions {
				if condition.Type == "ReplicaFailure" && condition.Status == "True" && strings.Contains(condition.Message, "violates PodSecurity") {
					add("Pod Security rejected the app's Pods: " + condition.Message + "\n" +
						"Remove the disallowed setting, or ask a cluster admin to label the namespace with a less strict pod-security.kubernetes.io/enforce level.")
				}
			}
		case "Pod":
			statuses := append(item.Status.InitContainerStatuses, item.Status.ContainerStatuses...)
			for _, status := range statuses {
				waiting := status.State.Waiting
				if waiting == nil || waiting.Reason != "CreateContainerConfigError" || !strings.Contains(waiting.Message, "runAsNonRoot") {
					continue
				}
				if strings.Contains(waiting.Message, "non-numeric user") {
					add(fmt.Sprintf("Container %q cannot start: its image sets a non-numeric USER, so Kubernetes cannot verify it runs as non-root.\n"+
						"Use a numeric USER in the image, or set the UID with --set podSecurityContext.runAsUser=<uid>.", status.Name))
				} else {
					add(fmt.Sprintf("Container %q cannot start: its image runs as root, and Podplane runs containers as non-root by default.\n"+
						"Use a non-root USER in the image, or opt out with --set podSecurityContext.runAsNonRoot=false.", status.Name))
				}
			}
		}
	}
	return problems
}

// containerStatus is the Pod container status subset used to find
// containers that cannot start.
type containerStatus struct {
	Name  string `json:"name"`
	State struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting"`
	} `json:"state"`
}
