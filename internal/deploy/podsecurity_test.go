// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"strings"
	"testing"
)

func TestPodSecurityProblemsFromList(t *testing.T) {
	// Messages use the formats of the Kubernetes ReplicaSet controller and kubelet.
	list := `{"kind": "List", "items": [
	  {"kind": "Deployment", "metadata": {"name": "api"}, "status": {"conditions": [
	    {"type": "Available", "status": "False", "message": "Deployment does not have minimum availability."},
	    {"type": "ReplicaFailure", "status": "True", "reason": "FailedCreate",
	     "message": "pods \"api-7d9c-x2x\" is forbidden: violates PodSecurity \"baseline:latest\": hostPath volumes (volume \"h\")"}]}},
	  {"kind": "Deployment", "metadata": {"name": "native"}, "status": {"conditions": [
	    {"type": "ReplicaFailure", "status": "True", "reason": "FailedCreate",
	     "message": "pods \"native-5c4d-x\" is forbidden: ValidatingAdmissionPolicy 'platform-runtime-class-vap' with binding 'platform-runtime-class-vapb' denied request: RuntimeClass \"runc\" requires namespace \"team-a\" to have the label runtime.podplane.dev/runc=allowed, which only a cluster administrator can grant. Otherwise set runtimeClassName to \"gvisor\", which is also the default outside platform runc namespaces."}]}},
	  {"kind": "Deployment", "metadata": {"name": "quota"}, "status": {"conditions": [
	    {"type": "ReplicaFailure", "status": "True", "reason": "FailedCreate",
	     "message": "pods \"quota-1\" is forbidden: exceeded quota: compute"}]}},
	  {"kind": "Pod", "metadata": {"name": "api-1"}, "status": {
	    "initContainerStatuses": [{"name": "migrate", "state": {"waiting": {"reason": "CreateContainerConfigError",
	      "message": "container has runAsNonRoot and image has non-numeric user (app), cannot verify user is non-root (pod: \"api-1_team-a(uid)\", container: migrate)"}}}],
	    "containerStatuses": [{"name": "api-container", "state": {"waiting": {"reason": "CreateContainerConfigError",
	      "message": "container has runAsNonRoot and image will run as root (pod: \"api-1_team-a(uid)\", container: api-container)"}}}]}},
	  {"kind": "Pod", "metadata": {"name": "api-2"}, "status": {
	    "containerStatuses": [
	      {"name": "api-container", "state": {"waiting": {"reason": "CreateContainerConfigError",
	        "message": "container has runAsNonRoot and image will run as root (pod: \"api-2_team-a(uid)\", container: api-container)"}}},
	      {"name": "envoy", "state": {"waiting": {"reason": "CreateContainerConfigError",
	        "message": "configmap \"api-envoy\" not found"}}}]}}
	]}`

	problems := podSecurityProblemsFromList([]byte(list))
	want := []string{
		`violates PodSecurity "baseline:latest": hostPath volumes`,
		`The namespace does not allow the runc runtime: pods "native-5c4d-x" is forbidden`,
		`Container "migrate" cannot start: its image sets a non-numeric USER`,
		`Container "api-container" cannot start: its image runs as root`,
	}
	if len(problems) != len(want) {
		t.Fatalf("got %d problems, want %d (quota and configmap errors ignored, replicas deduplicated):\n%s", len(problems), len(want), strings.Join(problems, "\n"))
	}
	for i, fragment := range want {
		if !strings.Contains(problems[i], fragment) {
			t.Errorf("problem %d = %q, want it to contain %q", i, problems[i], fragment)
		}
	}
	if !strings.Contains(problems[1], "Deploy without --set runtimeClassName=runc") {
		t.Errorf("runc problem should explain how to use gVisor: %q", problems[1])
	}
	if !strings.Contains(problems[3], "--set podSecurityContext.runAsNonRoot=false") {
		t.Errorf("root image problem should explain the opt-out: %q", problems[3])
	}
}
