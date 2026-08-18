// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"slices"
	"testing"
)

// TestCredentialExecArgsMarksOnlyLocalCredentials verifies the hook boundary encodes locality explicitly.
func TestCredentialExecArgsMarksOnlyLocalCredentials(t *testing.T) {
	localArgs := credentialExecArgs("podplane", "test-user", "default", true)
	if !slices.Contains(localArgs, "--exec-arg=--local") {
		t.Fatalf("local args = %q; want --local", localArgs)
	}
	remoteArgs := credentialExecArgs("podplane", "test-user", "default", false)
	if slices.Contains(remoteArgs, "--exec-arg=--local") {
		t.Fatalf("remote args = %q; do not want --local", remoteArgs)
	}
}

// TestCredentialExecCommandExactName verifies exact kubeconfig user matching.
func TestCredentialExecCommandExactName(t *testing.T) {
	name := `podplane-user:'"\\-cluster`
	data := []byte(`{"users":[{"name":"podplane-user:'\"\\\\-cluster","user":{"exec":{"command":"/path/with\\\\slash/podplane"}}}]}`)
	got, found, err := credentialExecCommand(data, name)
	if err != nil || !found || got != `/path/with\\slash/podplane` {
		t.Fatalf("credentialExecCommand = %q, %v, %v", got, found, err)
	}
	if _, found, _ := credentialExecCommand(data, name+"x"); found {
		t.Fatal("inexact name matched")
	}
}
