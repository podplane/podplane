// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/podplane/podplane/internal/clusterconfig"
)

// TestGenerateClusterModeLifecycle exercises the generated guard with real local
// state only: no external providers, modules, backends, or cloud credentials.
func TestGenerateClusterModeLifecycle(t *testing.T) {
	if os.Getenv("TFGEN_MODE_PROOF") != "1" {
		t.Skip("set TFGEN_MODE_PROOF=1 to exercise installed tofu and terraform")
	}
	for _, engine := range []string{"tofu", "terraform"} {
		t.Run(engine, func(t *testing.T) {
			binary, err := exec.LookPath(engine)
			if err != nil {
				t.Fatal(err)
			}
			for _, nat := range []string{"provider", "nstance"} {
				for _, exposure := range []string{"nlb", "tunnel"} {
					t.Run(nat+"-"+exposure, func(t *testing.T) {
						dir := t.TempDir()
						write := func(nat, exposure string, revision int) {
							t.Helper()
							cfg := sampleClusterConfig()
							cfg.Cluster.NAT.Mode = nat
							if nat == "nstance" {
								cfg.Cluster.Providers[0].Zones["us-east-1a"][1].Public = true
								cfg.Cluster.NAT.Groups = map[string]clusterconfig.NATGroup{"nat": {Arch: "arm64", InstanceType: "t4g.small", SubnetPool: "public"}}
								cfg.Cluster.NAT.Tenants = map[string]clusterconfig.TenantNAT{"default": {Group: "nat", InstanceTypeLadder: []string{"t4g.small"}}}
							}
							if exposure == "tunnel" {
								cfg.Cluster.Tunnel = &clusterconfig.Tunnel{Kind: "cloudflare", ID: "cbe15ed8-50dc-4e69-a1d0-9e1909ec4ce8", CredentialSecret: "tunnel-credentials"}
								cfg.Cluster.Providers[0].LoadBalancers = nil
								cfg.Cluster.Kubernetes.APILoadBalancer = ""
								cfg.Cluster.Kubernetes.APIPort = 443
							}
							files, err := GenerateCluster(filepath.Join(dir, "podplane.cluster.jsonc"), cfg, testClusterOptions(cfg))
							if err != nil {
								t.Fatal(err)
							}
							fixture := hclwrite.NewEmptyFile()
							for _, file := range files {
								if file.Type != FileTypeTerraform {
									continue
								}
								parsed, diags := hclwrite.ParseConfig([]byte(file.Content), file.Name, hcl.InitialPos)
								if diags.HasErrors() {
									t.Fatal(diags)
								}
								for _, b := range parsed.Body().Blocks() {
									labels := b.Labels()
									if (b.Type() == "variable" && len(labels) == 1 && labels[0] == "nat_mode") || (b.Type() == "resource" && len(labels) == 2 && labels[0] == "terraform_data" && labels[1] == "deployment_modes") {
										fixture.Body().AppendBlock(b)
									}
								}
							}
							// A changing dependent stands in for normal deployment updates and sleep.
							data := append(fixture.Bytes(), []byte(fmt.Sprintf("\nresource \"terraform_data\" \"deployment\" {\n input = %d\n depends_on = [terraform_data.deployment_modes]\n}\n", revision))...)
							if err := os.WriteFile(filepath.Join(dir, "main.tf"), data, 0600); err != nil {
								t.Fatal(err)
							}
						}
						run := func(wantError string, args ...string) string {
							t.Helper()
							cmd := exec.Command(binary, args...)
							cmd.Dir = dir
							cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1"}
							out, err := cmd.CombinedOutput()
							if (wantError == "" && err != nil) || (wantError != "" && (err == nil || !strings.Contains(string(out), wantError))) {
								t.Fatalf("%s %v: %v\n%s", engine, args, err, out)
							}
							return string(out)
						}
						write(nat, exposure, 1)
						run("", "init", "-backend=false", "-input=false", "-no-color")
						run("", "apply", "-auto-approve", "-no-color")
						run("", "apply", "-auto-approve", "-no-color")
						write(nat, exposure, 2)
						run("", "apply", "-auto-approve", "-no-color")
						otherNAT := "provider"
						if nat == otherNAT {
							otherNAT = "nstance"
						}
						run("Changing NAT mode", "apply", "-auto-approve", "-no-color", "-var=nat_mode="+otherNAT)
						write(otherNAT, exposure, 3)
						run("Changing NAT mode", "apply", "-auto-approve", "-no-color")
						otherExposure := "nlb"
						if exposure == otherExposure {
							otherExposure = "tunnel"
						}
						write(nat, otherExposure, 3)
						run("Changing exposure mode", "apply", "-auto-approve", "-no-color")
						state := run("", "state", "show", "-no-color", "terraform_data.deployment")
						if !strings.Contains(strings.Join(strings.Fields(state), " "), "input = 2") {
							t.Fatalf("rejected switches changed dependent deployment: %s", state)
						}
						write(otherNAT, otherExposure, 3)
						run("", "destroy", "-auto-approve", "-no-color")
						run("", "apply", "-auto-approve", "-no-color")
						run("", "destroy", "-auto-approve", "-no-color")
					})
				}
			}
		})
	}
}
