// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/kubectl"
)

// WorkloadCheck returns a health check for a workload's Kubernetes readiness.
func WorkloadCheck(kubeContext, kubeconfig, namespace, resource, name string, required bool) Check {
	displayKind := strings.TrimSuffix(resource, "s")
	return Check{
		Key:      key(namespace, resource, name),
		Name:     name,
		Kind:     displayKind,
		Required: required,
		Run: func(ctx context.Context) Result {
			return readWorkload(ctx, kubeContext, kubeconfig, namespace, resource, name)
		},
	}
}

// readWorkload reads one Kubernetes workload and maps its status to a health
// result.
func readWorkload(ctx context.Context, kubeContext, kubeconfig, namespace, resource, name string) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}
	}
	args := kubectl.Args(kubeContext, kubeconfig)
	args = append(args, "-n", namespace, "get", resource, name, "-o", "json")
	var stdout, stderr bytes.Buffer
	cmd := execwrap.Command("kubectl", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "NotFound") || strings.Contains(stderr.String(), "not found") {
			return Result{Status: StatusPending, Message: fmt.Sprintf("waiting for %s to be created", resource)}
		}
		return Result{Err: &kubectl.Error{Stage: "get " + resource, Err: err, Stderr: stderr.String()}}
	}

	var obj workloadObject
	if err := json.Unmarshal(stdout.Bytes(), &obj); err != nil {
		return Result{Err: fmt.Errorf("decode %s status: %w", resource, err)}
	}
	ready, message := workloadReady(resource, obj.Status)
	if ready {
		return Result{Exists: true, Ready: true, Status: StatusReady, Message: message}
	}
	return Result{Exists: true, Status: StatusPending, Message: message}
}

// workloadReady reports whether a workload status has reached its desired
// ready/available count.
func workloadReady(resource string, status workloadStatus) (bool, string) {
	switch resource {
	case "deployment", "deploy", "deployments":
		desired := status.Replicas
		if desired == 0 {
			desired = 1
		}
		ready := status.ReadyReplicas
		if ready >= desired && status.UpdatedReplicas >= desired && status.AvailableReplicas >= desired {
			return true, fmt.Sprintf("%d/%d replicas ready", ready, desired)
		}
		return false, fmt.Sprintf("%d/%d replicas ready", ready, desired)
	case "daemonset", "ds", "daemonsets":
		desired := status.DesiredNumberScheduled
		ready := status.NumberReady
		if desired > 0 && ready >= desired && status.UpdatedNumberScheduled >= desired && status.NumberAvailable >= desired {
			return true, fmt.Sprintf("%d/%d pods ready", ready, desired)
		}
		return false, fmt.Sprintf("%d/%d pods ready", ready, desired)
	case "statefulset", "sts", "statefulsets":
		desired := status.Replicas
		if desired == 0 {
			desired = 1
		}
		ready := status.ReadyReplicas
		if ready >= desired && status.UpdatedReplicas >= desired {
			return true, fmt.Sprintf("%d/%d replicas ready", ready, desired)
		}
		return false, fmt.Sprintf("%d/%d replicas ready", ready, desired)
	default:
		return false, "unsupported workload kind"
	}
}

type workloadObject struct {
	Status workloadStatus `json:"status"`
}

type workloadStatus struct {
	Replicas               int `json:"replicas"`
	ReadyReplicas          int `json:"readyReplicas"`
	UpdatedReplicas        int `json:"updatedReplicas"`
	AvailableReplicas      int `json:"availableReplicas"`
	DesiredNumberScheduled int `json:"desiredNumberScheduled"`
	NumberReady            int `json:"numberReady"`
	UpdatedNumberScheduled int `json:"updatedNumberScheduled"`
	NumberAvailable        int `json:"numberAvailable"`
}
