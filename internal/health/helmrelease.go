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

// HelmReleaseRef identifies one Flux HelmRelease to observe.
type HelmReleaseRef struct {
	Name      string
	Namespace string
	Kind      string
}

// HelmReleaseStatus is the current observed status of one HelmRelease.
type HelmReleaseStatus struct {
	Ref     HelmReleaseRef
	Exists  bool
	Ready   bool
	Status  string
	Message string
}

// HelmReleaseCheck returns a health check for a Flux HelmRelease Ready condition.
func HelmReleaseCheck(kubeContext, kubeconfig string, ref HelmReleaseRef, required bool) Check {
	return Check{
		Key:      key(ref.Namespace, "helmrelease", ref.Name),
		Name:     ref.Name,
		Kind:     ref.Kind,
		Required: required,
		Run: func(ctx context.Context) Result {
			statuses, err := ReadHelmReleaseStatuses(ctx, kubeContext, kubeconfig, []HelmReleaseRef{ref})
			if err != nil {
				return Result{Err: err}
			}
			status := statuses[key(ref.Namespace, "helmrelease", ref.Name)]
			return Result{Exists: status.Exists, Ready: status.Ready, Status: Status(status.Status), Message: status.Message}
		},
	}
}

// ReadHelmReleaseStatuses reads Flux HelmRelease statuses once.
func ReadHelmReleaseStatuses(ctx context.Context, kubeContext, kubeconfig string, refs []HelmReleaseRef) (map[string]HelmReleaseStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statuses := map[string]HelmReleaseStatus{}
	for _, ref := range refs {
		statuses[key(ref.Namespace, "helmrelease", ref.Name)] = HelmReleaseStatus{
			Ref:     ref,
			Status:  "Pending",
			Message: "waiting for HelmRelease to be created",
		}
	}
	if len(refs) == 0 {
		return statuses, nil
	}

	args := kubectl.Args(kubeContext, kubeconfig)
	args = append(args, "get", "helmreleases.helm.toolkit.fluxcd.io", "-A", "-o", "json")
	var stdout, stderr bytes.Buffer
	cmd := execwrap.Command("kubectl", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &kubectl.Error{Stage: "get helmreleases", Err: err, Stderr: stderr.String()}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var list helmReleaseList
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		return nil, fmt.Errorf("decode HelmRelease status: %w", err)
	}
	for _, hr := range list.Items {
		refKey := key(hr.Metadata.Namespace, "helmrelease", hr.Metadata.Name)
		status, ok := statuses[refKey]
		if !ok {
			continue
		}
		status.Exists = true
		status.Ready, status.Status, status.Message = HelmReleaseReadyStatus(hr.Status.Conditions)
		statuses[refKey] = status
	}
	return statuses, nil
}

// HelmReleaseReadyStatus converts Flux HelmRelease Ready conditions into a
// compact readiness tuple.
func HelmReleaseReadyStatus(conditions []HelmReleaseCondition) (bool, string, string) {
	var ready *HelmReleaseCondition
	for i := range conditions {
		if conditions[i].Type == "Ready" {
			ready = &conditions[i]
			break
		}
	}
	if ready == nil {
		return false, "Reconciling", "waiting for Ready condition"
	}
	message := strings.TrimSpace(ready.Message)
	if ready.Status == "True" {
		if message == "" {
			message = "HelmRelease is ready"
		}
		return true, "Ready", message
	}
	status := ready.Reason
	if status == "" {
		status = "Reconciling"
	}
	if message == "" {
		message = "waiting for HelmRelease to become ready"
	}
	return false, status, message
}

type helmReleaseList struct {
	Items []helmRelease `json:"items"`
}

type helmRelease struct {
	Metadata helmReleaseMetadata `json:"metadata"`
	Status   helmReleaseStatus   `json:"status"`
}

type helmReleaseMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type helmReleaseStatus struct {
	Conditions []HelmReleaseCondition `json:"conditions"`
}

// HelmReleaseCondition is one Flux HelmRelease condition.
type HelmReleaseCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}
