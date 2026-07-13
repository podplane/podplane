// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
	"github.com/spf13/cobra"
)

const validClusterConfigJSON = `{
  "cluster": {
    "id": "test-cluster",
    "oidc": { "issuer_url": "https://auth.example.com" },
    "pools": {
      "control-plane": { "arch": "arm64", "instance_type": "t4g.medium", "size": 1 }
    },
    "providers": [{
      "kind": "aws",
      "region": "us-east-1",
      "account": "123456789012",
      "vpc": { "v4cidr": "172.18.0.0/16", "v6cidr": "auto" },
      "zones": {
        "us-east-1a": [
          { "v4cidr": "172.18.10.0/28", "services": ["nat", "nlb"], "public": true },
          { "v4cidr": "172.18.20.0/28", "services": ["nstance"] },
          { "v4cidr": "172.18.1.0/24", "pool": "control-plane" }
        ]
      },
      "load_balancer": {
        "public": true,
        "listeners": [{ "port": 6443, "pool": "control-plane" }]
      }
    }],
    "kubernetes": {
      "cluster_cidr": ["100.64.0.0/10"],
      "service_cidr": ["198.18.0.0/15"]
    }
  }
}`

// TestClusterCreateNoApplyGeneratesTerraform verifies cluster create writes
// managed Terraform without invoking OpenTofu/Terraform.
func TestClusterCreateNoApplyGeneratesTerraform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.cluster.jsonc")
	if err := os.WriteFile(path, []byte(validClusterConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newClusterCreateCmd(&config.Config{})
	cmd.SetArgs([]string{"--cluster-config", path, "--no-apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cluster create --no-apply returned error: %v", err)
	}
	for _, name := range []string{
		"podplane.cluster.schema.json",
		"podplane.cluster.main.tf",
		"podplane.cluster.buckets.tf",
		"podplane.cluster.roles.tf",
		"podplane.cluster.inputs.runtime.tf",
		"podplane.cluster.inputs.vm.tf",
		"podplane.cluster.inputs.infra.tf",
		"podplane.cluster.outputs.tf",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("AWS cluster file %s was not generated: %v", name, err)
		}
	}
}

// TestClusterDeleteNoApplyValidatesOnly verifies cluster delete no-apply
// validates the config without invoking destroy dependencies.
func TestClusterDeleteNoApplyValidatesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "podplane.cluster.jsonc")
	if err := os.WriteFile(path, []byte(validClusterConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newClusterDeleteCmd(&config.Config{})
	cmd.SetArgs([]string{"--cluster-config", path, "--no-apply"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cluster delete --no-apply returned error: %v", err)
	}
}

func TestDeploySecretSetArgsUseSecretProviderBindingItems(t *testing.T) {
	summary := config.ClusterSummary{
		ID: "test-cluster",
		Secrets: clusterconfig.Secrets{
			DefaultProvider: "aws-secrets-manager",
			Providers: map[string]clusterconfig.SecretsProvider{
				"aws-secrets-manager": {Kind: "aws", ObjectType: "secretsmanager"},
			},
		},
	}

	got, err := deploySecretSetArgs(summary, "aok-source-controller", []string{"github-private-key", "webhook-secret"})
	if err != nil {
		t.Fatalf("deploySecretSetArgs error = %v", err)
	}
	want := []string{
		"secrets[0].bindingName=aok-source-controller",
		"secrets[0].providerName=aws-secrets-manager",
		"secrets[0].mountPath=/var/run/podplane/secrets",
		"secrets[0].items[0].key=github-private-key",
		"secrets[0].items[1].key=webhook-secret",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deploySecretSetArgs = %#v, want %#v", got, want)
	}
}

func TestDeploySecretSetArgsRequiresDefaultProvider(t *testing.T) {
	_, err := deploySecretSetArgs(config.ClusterSummary{ID: "test-cluster"}, "app", []string{"github-private-key"})
	if err == nil || !strings.Contains(err.Error(), "default secrets provider") {
		t.Fatalf("deploySecretSetArgs error = %v, want default provider error", err)
	}
}

func TestSecretCommandUsesSingularName(t *testing.T) {
	cmd := newSecretCmd(&config.Config{})
	if got, want := cmd.Name(), "secret"; got != want {
		t.Fatalf("secret command name = %q, want %q", got, want)
	}
	if got, want := cmd.Short, "Manage application secrets"; got != want {
		t.Fatalf("secret command short = %q, want %q", got, want)
	}
}

func TestSilenceUsageAfterValidationSuppressesRuntimeUsage(t *testing.T) {
	cmd := testUsageCommand(t)
	cmd.SetArgs([]string{"run", "ok"})
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected runtime error")
	}
	output := stdout.String() + stderr.String()
	if strings.Contains(output, "Usage:") {
		t.Fatalf("runtime error printed usage: %q", output)
	}
}

func TestSilenceUsageAfterValidationKeepsArgValidationUsage(t *testing.T) {
	cmd := testUsageCommand(t)
	cmd.SetArgs([]string{"run"})
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected argument validation error")
	}
	output := stdout.String() + stderr.String()
	if !strings.Contains(output, "Usage:") {
		t.Fatalf("argument validation error did not print usage: %q", output)
	}
}

func testUsageCommand(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{
		Use: "test",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
		},
	}
	cmd := &cobra.Command{
		Use:  "run [arg]",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("runtime failed")
		},
	}
	root.AddCommand(cmd)
	return root
}
