// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterspec

import (
	"strings"
	"testing"
)

// TestMutableEnvRenderSeparatesMutableAndImmutableSSHKeys verifies that only
// mutable SSH keys are included in mutable.env.
func TestMutableEnvRenderSeparatesMutableAndImmutableSSHKeys(t *testing.T) {
	env := MutableEnv{
		"SSH_AUTHORIZED_KEYS":           "ssh-ed25519 AAAAmutable",
		"IMMUTABLE_SSH_AUTHORIZED_KEYS": "ssh-ed25519 AAAAimmutable",
		"REGISTRY_HOSTNAME":             "registry.example.com",
	}
	env.ApplyDefaults("example-cluster")
	out, err := env.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "SSH_AUTHORIZED_KEYS='ssh-ed25519 AAAAmutable'") {
		t.Fatalf("expected mutable SSH keys; got:\n%s", out)
	}
	if strings.Contains(out, "IMMUTABLE_SSH_AUTHORIZED_KEYS") {
		t.Fatalf("immutable SSH keys must not be rendered into mutable.env; got:\n%s", out)
	}
}

// TestMutableEnvRejectsInvalidValues verifies mutable.env validation.
func TestMutableEnvRejectsInvalidValues(t *testing.T) {
	tests := []MutableEnv{
		{"TELEMETRY_LOG_CLOUDINIT": "maybe", "REGISTRY_HOSTNAME": "registry.example.com"},
		{"TELEMETRY_LOG_SERVICES": "kubelet,ssh.service", "REGISTRY_HOSTNAME": "registry.example.com"},
		{"OIDC_CA_CERT": "line1\nline2", "REGISTRY_HOSTNAME": "registry.example.com"},
		{"KUBE_NODE_CIDR_MASK_SIZE_IPV4": "33", "REGISTRY_HOSTNAME": "registry.example.com"},
		{"KUBE_NODE_CIDR_MASK_SIZE_IPV6": "-1", "REGISTRY_HOSTNAME": "registry.example.com"},
	}
	for _, env := range tests {
		env.ApplyDefaults("example-cluster")
		if _, err := env.Render(); err == nil {
			t.Fatalf("expected validation error for %#v", env)
		}
	}
}

// TestMutableEnvObjectStorageSetters verifies shared storage convenience setters.
func TestMutableEnvObjectStorageSetters(t *testing.T) {
	env := MutableEnv{}
	env.SetObjectStorageEndpoint("https://object.example")
	env.SetObjectStorageRegion("region-1")

	if env["NETSY_ENDPOINT"] != "https://object.example" || env["TELEMETRY_S3_ENDPOINT"] != "https://object.example" || env["REGISTRY_ENDPOINT"] != "https://object.example" {
		t.Fatalf("expected shared object storage endpoint to be set on all components: %#v", env)
	}
	if env["NETSY_REGION"] != "region-1" || env["TELEMETRY_S3_REGION"] != "region-1" || env["REGISTRY_REGION"] != "region-1" {
		t.Fatalf("expected shared object storage region to be set on all components: %#v", env)
	}
}

// TestMutableEnvAppliesKubernetesCIDRDefaults verifies vmconfig-owned network defaults.
func TestMutableEnvAppliesKubernetesCIDRDefaults(t *testing.T) {
	env := MutableEnv{"REGISTRY_HOSTNAME": "registry.example.com"}
	env.ApplyDefaults("example-cluster")
	if env["KUBE_CLUSTER_CIDR"] != "100.64.0.0/10,fd64::/48" {
		t.Fatalf("KUBE_CLUSTER_CIDR = %q", env["KUBE_CLUSTER_CIDR"])
	}
	if env["KUBE_SERVICE_CLUSTER_IP_RANGE"] != "198.18.0.0/15,fdc6::/108" {
		t.Fatalf("KUBE_SERVICE_CLUSTER_IP_RANGE = %q", env["KUBE_SERVICE_CLUSTER_IP_RANGE"])
	}
	if env["KUBE_NODE_CIDR_MASK_SIZE_IPV4"] != "24" || env["KUBE_NODE_CIDR_MASK_SIZE_IPV6"] != "64" {
		t.Fatalf("node CIDR mask defaults = %q/%q", env["KUBE_NODE_CIDR_MASK_SIZE_IPV4"], env["KUBE_NODE_CIDR_MASK_SIZE_IPV6"])
	}
	if env["OIDC_SIGNING_ALGS"] != "RS256" {
		t.Fatalf("OIDC_SIGNING_ALGS = %q", env["OIDC_SIGNING_ALGS"])
	}
}
