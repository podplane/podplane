// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Package clustersleep requests sleep for Nstance-managed clusters.
package clustersleep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/podplane/podplane/internal/kubectl"
)

// Options selects a Kubernetes context and supplies force confirmation UI.
type Options struct {
	Context     string
	Kubeconfig  string
	Force       bool
	AutoApprove bool
	Warn        func(string)
	Confirm     func(string) (bool, error)
}

// Request authorizes and patches one Nstance-managed CAPI Cluster, then
// returns without polling. Other annotations, including progress, are untouched.
func Request(ctx context.Context, opts Options) error {
	selected, err := kubectl.ResolveContext(ctx, opts.Context, opts.Kubeconfig)
	if err != nil {
		return fmt.Errorf("read sleep context: %w", err)
	}
	clusterID, local := selected.ClusterID()
	if local || clusterID == "" || selected.Cluster != kubectl.ClusterKey(clusterID, false) {
		return fmt.Errorf("context %q does not reference a Podplane cloud cluster", selected.Name)
	}
	run := func(input []byte, args ...string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		command := exec.CommandContext(ctx, "kubectl", append(kubectl.Args(selected.Name, opts.Kubeconfig), args...)...)
		command.Stdin = bytes.NewReader(input)
		var out, stderr bytes.Buffer
		command.Stdout, command.Stderr = &out, &stderr
		err := command.Run()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("invoke kubectl: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return out.Bytes(), nil
	}
	// Nstance names CAPI Clusters <clusterID>--<tenant> in the operator's
	// namespace. Discover the resource and refuse ambiguous multi-tenant targets.
	raw, err := run(nil, "get", "clusters.cluster.x-k8s.io", "--all-namespaces", "--output=json")
	if err != nil {
		return fmt.Errorf("discover Nstance CAPI Cluster: %w", err)
	}
	// target contains the CAPI identity and infrastructure reference used to select a sleep recipient.
	type target struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			InfrastructureRef struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"infrastructureRef"`
		} `json:"spec"`
	}
	var list struct {
		Items []target `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("parse CAPI Clusters: %w", err)
	}
	var candidates []target
	prefix := clusterID + "--"
	for _, item := range list.Items {
		if strings.HasPrefix(item.Metadata.Name, prefix) && len(item.Metadata.Name) > len(prefix) && item.Metadata.Namespace != "" && item.Spec.InfrastructureRef.Kind == "NstanceCluster" && item.Spec.InfrastructureRef.Name == item.Metadata.Name {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) != 1 {
		return fmt.Errorf("expected one Nstance CAPI Cluster for %q, found %d; refusing sleep", clusterID, len(candidates))
	}
	name, namespace := candidates[0].Metadata.Name, candidates[0].Metadata.Namespace
	if opts.Force {
		warning := "Forced sleep: active workloads and connections may be interrupted."
		if opts.Warn != nil {
			opts.Warn(warning)
		}
		if !opts.AutoApprove {
			if opts.Confirm == nil {
				return fmt.Errorf("forced sleep requires explicit confirmation or --auto-approve (-y)")
			}
			ok, err := opts.Confirm(fmt.Sprintf("Force sleep of %s/%s?", namespace, name))
			if err != nil {
				return fmt.Errorf("confirm forced sleep: %w", err)
			}
			if !ok {
				return fmt.Errorf("sleep cancelled")
			}
		}
	}
	review, _ := json.Marshal(map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": map[string]string{
			"group": "cluster.x-k8s.io", "resource": "clusters", "verb": "patch", "name": name, "namespace": namespace,
		}},
	})
	raw, err = run(review, "create", "--filename=-", "--output=json")
	if err != nil {
		return fmt.Errorf("review CAPI Cluster patch access: %w", err)
	}
	var access struct {
		Status struct {
			Allowed         bool   `json:"allowed"`
			Denied          bool   `json:"denied"`
			Reason          string `json:"reason"`
			EvaluationError string `json:"evaluationError"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &access); err != nil {
		return fmt.Errorf("parse patch access review: %w", err)
	}
	if !access.Status.Allowed || access.Status.Denied || access.Status.EvaluationError != "" {
		return fmt.Errorf("not authorized to patch CAPI Cluster %s/%s: %s %s", namespace, name, access.Status.Reason, access.Status.EvaluationError)
	}
	request := time.Now().UTC().Format(time.RFC3339)
	if opts.Force {
		request = "force:" + request
	}
	patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{"nstance.dev/sleep-request": request}}})
	// JSON merge patch merges the annotation map, preserving progress and all
	// other keys even if the operator changes them concurrently.
	if _, err := run(nil, "patch", "clusters.cluster.x-k8s.io", name, "--namespace", namespace, "--type=merge", "--patch", string(patch)); err != nil {
		return fmt.Errorf("request sleep of CAPI Cluster %s/%s: %w", namespace, name, err)
	}
	return nil
}
