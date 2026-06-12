// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package deploy

import (
	"testing"
	"time"
)

func TestHelmUpgradeInstallArgsWaitsForWorkloadReadiness(t *testing.T) {
	t.Parallel()
	got := helmUpgradeInstallArgs(Options{Name: "hello", ChartPath: "/charts/web", Wait: true, Timeout: 2 * time.Minute}, "/tmp/values.json")
	want := []string{"upgrade", "--install", "hello", "/charts/web", "-f", "/tmp/values.json", "--wait", "--timeout", "2m0s"}
	assertStringSlicesEqual(t, got, want)
}

func TestHelmUpgradeInstallArgsCanSkipWait(t *testing.T) {
	t.Parallel()
	got := helmUpgradeInstallArgs(Options{Name: "hello", ChartPath: "/charts/web", Timeout: 2 * time.Minute}, "/tmp/values.json")
	want := []string{"upgrade", "--install", "hello", "/charts/web", "-f", "/tmp/values.json"}
	assertStringSlicesEqual(t, got, want)
}

func TestHelmUpgradeInstallArgsIncludesOptionalFlags(t *testing.T) {
	t.Parallel()
	got := helmUpgradeInstallArgs(Options{
		Name:       "hello",
		ChartPath:  "/charts/web",
		Set:        []string{"app.port=8080"},
		Namespace:  "apps",
		Context:    "dev",
		Kubeconfig: "/tmp/kubeconfig",
		Wait:       true,
		Timeout:    10 * time.Minute,
	}, "/tmp/values.json")
	want := []string{
		"upgrade", "--install", "hello", "/charts/web", "-f", "/tmp/values.json", "--wait", "--timeout", "10m0s",
		"--set", "app.port=8080",
		"--namespace", "apps", "--create-namespace",
		"--kube-context", "dev",
		"--kubeconfig", "/tmp/kubeconfig",
	}
	assertStringSlicesEqual(t, got, want)
}
