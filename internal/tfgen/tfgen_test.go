// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/oidcconfig"
)

// sampleVMConfigManifest returns a small vmconfig manifest for tfgen tests.
func sampleVMConfigManifest(kind string, arch string) *deps.Manifest {
	return &deps.Manifest{VMConfig: deps.VMConfig{
		Version: "2026.01.01",
		Kind:    kind,
		OS: deps.OSInfo{
			Name: deps.OS,
			Arch: arch,
		},
		Dependencies: map[string]deps.Dependency{
			"runc": {
				Version: "1.2.3",
				URL:     "https://example.com/deps/runc",
				Digest:  "sha256:" + strings.Repeat("a", 64),
			},
			"vmconfig": {
				Version: "2026.01.01",
				URL:     "https://example.com/deps/vmconfig.tar.gz",
				Type:    "tar.gz",
				Digest:  "sha256:" + strings.Repeat("b", 64),
			},
		},
	}}
}

// testClusterOptions returns fixed dependency inputs so cluster tfgen tests do
// not read the local deps cache or fetch remote manifests.
func testClusterOptions() ClusterOptions {
	manifest, err := json.MarshalIndent(sampleVMConfigManifest("knc", "arm64"), "", "  ")
	if err != nil {
		panic(err)
	}
	return ClusterOptions{
		DepsMirrorURL: "https://deps.podplane.dev",
		VMConfigManifests: []VMConfigManifest{
			{Kind: "knc", Arch: "arm64", Filename: "vmconfig_knc_debian-13_arm64.json", JSON: append(manifest, '\n')},
		},
	}
}

// TestGenerateAWSClusterTerraform verifies the generated AWS cluster Terraform
// contains the expected provider modules and group references.
func TestGenerateAWSClusterTerraform(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID:   "test-cluster",
		Name: "Test Cluster",
		OIDC: clusterconfig.OIDC{IssuerURL: "https://auth.example.com", SigningAlgs: []string{"RS256", "ES256"}},
		Kubernetes: clusterconfig.Kubernetes{
			APIPort: 7443,
		},
		Seed: clusterconfig.Seed{Name: "recommended", Version: "v1.0.0-1", Digest: "sha512:" + strings.Repeat("0", 128)},
		Pools: map[string]clusterconfig.Pool{
			"control-plane": {Arch: "arm64", InstanceType: "t4g.medium", Size: 1},
		},
		Providers: []clusterconfig.Provider{{
			Kind:    "aws",
			Region:  "us-east-1",
			Account: "123456789012",
			VPC:     clusterconfig.VPC{V4CIDR: "172.18.0.0/16", V6CIDR: "auto"},
			Zones: map[string][]clusterconfig.Subnet{
				"us-east-1a": {
					{V4CIDR: "172.18.10.0/28", Services: []string{"nat", "nlb"}, Public: true},
					{V4CIDR: "172.18.20.0/28", Services: []string{"nstance"}},
					{V4CIDR: "172.18.1.0/24", Pool: "control-plane"},
				},
			},
			LoadBalancer: clusterconfig.LoadBalancer{
				Public:    true,
				Listeners: []clusterconfig.Listener{{Port: 7443, Pool: "control-plane"}},
			},
		}},
	}}
	files, err := GenerateCluster(filepath.Join(t.TempDir(), "podplane.cluster.jsonc"), cfg, testClusterOptions())
	if err != nil {
		t.Fatalf("GenerateCluster returned error: %v", err)
	}
	if len(files) != 8 {
		t.Fatalf("len(files) = %d, want 8", len(files))
	}
	contents := fileContents(files)
	for _, name := range []string{
		"podplane.cluster.main.tf",
		"podplane.cluster.buckets.tf",
		"podplane.cluster.roles.tf",
		"podplane.cluster.inputs.runtime.tf",
		"podplane.cluster.inputs.vm.tf",
		"podplane.cluster.inputs.infra.tf",
		"podplane.cluster.outputs.tf",
		"podplane.cluster.vmconfig_knc_debian-13_arm64.json",
	} {
		if _, ok := contents[name]; !ok {
			t.Fatalf("generated files missing %s: %#v", name, files)
		}
	}
	assertExpectedTerraform(t, "podplane.cluster.main.expected.tf", contents["podplane.cluster.main.tf"])
	assertExpectedTerraform(t, "podplane.cluster.buckets.expected.tf", contents["podplane.cluster.buckets.tf"])
	assertExpectedTerraform(t, "podplane.cluster.roles.expected.tf", contents["podplane.cluster.roles.tf"])
	assertExpectedTerraform(t, "podplane.cluster.inputs.runtime.expected.tf", contents["podplane.cluster.inputs.runtime.tf"])
	assertExpectedTerraform(t, "podplane.cluster.inputs.vm.expected.tf", contents["podplane.cluster.inputs.vm.tf"])
	assertExpectedTerraform(t, "podplane.cluster.inputs.infra.expected.tf", contents["podplane.cluster.inputs.infra.tf"])
	assertExpectedTerraform(t, "podplane.cluster.outputs.expected.tf", contents["podplane.cluster.outputs.tf"])
	got := contents["podplane.cluster.main.tf"] + contents["podplane.cluster.buckets.tf"] + contents["podplane.cluster.roles.tf"] + contents["podplane.cluster.inputs.runtime.tf"] + contents["podplane.cluster.inputs.vm.tf"] + contents["podplane.cluster.inputs.infra.tf"] + contents["podplane.cluster.outputs.tf"]
	for _, want := range []string{
		`provider "aws"`,
		`module "network_123456789012_us_east_1"`,
		`source = "podplane/podplane"`,
		`resource "podplane_netsy_seed_s3" "cluster"`,
		`cluster_config_path = "${path.module}/podplane.cluster.jsonc"`,
		`bucket = aws_s3_bucket.podplane_cluster["netsy"].bucket`,
		`region = local.aws_region`,
		`certificates = local.certificates`,
		`templates = local.templates`,
		`data "podplane_userdata" "knc_arm64"`,
		`manifest_json = file("${path.module}/podplane.cluster.vmconfig_knc_debian-13_arm64.json")`,
		`immutable_ssh_authorized_keys = var.immutable_ssh_authorized_keys`,
		`enable_ssm = var.enable_ssm`,
		`content = base64encode(data.podplane_userdata.knc_arm64.content)`,
		`vars = local.mutable_env`,
		`"public-control-plane" = { ports = [7443], subnets = "public", public = true }`,
		`load_balancers = ["public-control-plane"]`,
		`REGISTRY_ASSUME_ROLE = aws_iam_role.podplane_cluster["registry-read-only"].arn`,
		`output "registry_read_write_role_arn"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated cluster tf missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "local.instance_vars") {
		t.Fatalf("generated cluster tf must not put immutable inputs in Nstance runtime vars:\n%s", got)
	}
	if strings.Contains(got, "IMMUTABLE_SSH_AUTHORIZED_KEYS=") {
		t.Fatalf("generated Terraform must not embed rendered userdata:\n%s", got)
	}
	if strings.Contains(got, "pool_disk_sizes") {
		t.Fatalf("generated Terraform must not advertise disk sizing as an independent override:\n%s", got)
	}
	infra := contents["podplane.cluster.inputs.infra.tf"]
	for _, want := range []string{`default = ""`, `default = "172.18.0.0/16"`, `subnets = {`} {
		if !strings.Contains(infra, want) {
			t.Fatalf("generated infra inputs missing %q:\n%s", want, infra)
		}
	}
	if strings.Contains(contents["podplane.cluster.main.tf"], "TELEMETRY_ENABLED = tostring(var.telemetry_enabled)") || !strings.Contains(contents["podplane.cluster.main.tf"], "if value != null") {
		t.Fatalf("generated mutable env must omit unset vmconfig-owned defaults:\n%s", contents["podplane.cluster.main.tf"])
	}
	if !strings.Contains(contents["podplane.cluster.vmconfig_knc_debian-13_arm64.json"], `"kind": "knc"`) {
		t.Fatalf("generated manifest copy is invalid:\n%s", contents["podplane.cluster.vmconfig_knc_debian-13_arm64.json"])
	}

	// CIDRs are runtime inputs. A JSON CIDR change must affect only runtime inputs.
	cfg.Cluster.Kubernetes.ClusterCIDR = []string{"100.64.0.0/10"}
	changedFiles, err := GenerateCluster(filepath.Join(t.TempDir(), "podplane.cluster.jsonc"), cfg, testClusterOptions())
	if err != nil {
		t.Fatalf("GenerateCluster after CIDR change returned error: %v", err)
	}
	changed := fileContents(changedFiles)
	if changed["podplane.cluster.inputs.runtime.tf"] == contents["podplane.cluster.inputs.runtime.tf"] {
		t.Fatal("Kubernetes CIDR change did not affect runtime impact inputs")
	}
	if changed["podplane.cluster.inputs.infra.tf"] != contents["podplane.cluster.inputs.infra.tf"] {
		t.Fatal("Kubernetes CIDR change unexpectedly affected infra impact inputs")
	}

	// Nstance uses empty strings, not null, as the managed/existing VPC
	// sentinels. Existing-VPC generation must clear the managed CIDR.
	cfg.Cluster.Providers[0].VPC.ID = "vpc-0123456789abcdef0"
	cfg.Cluster.Providers[0].VPC.V4CIDR = ""
	cfg.Cluster.Providers[0].VPC.V6CIDR = ""
	existingFiles, err := GenerateCluster(filepath.Join(t.TempDir(), "podplane.cluster.jsonc"), cfg, testClusterOptions())
	if err != nil {
		t.Fatalf("GenerateCluster for existing VPC returned error: %v", err)
	}
	existingInfra := fileContents(existingFiles)["podplane.cluster.inputs.infra.tf"]
	for _, want := range []string{`default = "vpc-0123456789abcdef0"`, "variable \"vpc_cidr_ipv4\" {\n  description = \"Managed VPC IPv4 CIDR; changing it may replace networking.\"\n  type = string\n  default = \"\""} {
		if !strings.Contains(existingInfra, want) {
			t.Fatalf("existing-VPC infra inputs missing %q:\n%s", want, existingInfra)
		}
	}
}

// fileContents indexes generated Terraform files by name.
func fileContents(files []File) map[string]string {
	contents := map[string]string{}
	for _, file := range files {
		contents[file.Name] = file.Content
	}
	return contents
}

// TestGenerateAWSClusterTerraformWithoutSeed verifies bare clusters do not
// upload an empty Netsy seed snapshot.
func TestGenerateAWSClusterTerraformWithoutSeed(t *testing.T) {
	cfg := &clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{
		ID:   "bare-cluster",
		Name: "Bare Cluster",
		OIDC: clusterconfig.OIDC{IssuerURL: "https://auth.example.com"},
		Pools: map[string]clusterconfig.Pool{
			"control-plane": {Arch: "arm64", InstanceType: "t4g.medium", Size: 1},
		},
		Providers: []clusterconfig.Provider{{
			Kind:    "aws",
			Region:  "us-east-1",
			Account: "123456789012",
			VPC:     clusterconfig.VPC{V4CIDR: "172.18.0.0/16"},
			Zones: map[string][]clusterconfig.Subnet{
				"us-east-1a": {{V4CIDR: "172.18.1.0/24", Pool: "control-plane"}},
			},
		}},
	}}
	files, err := GenerateCluster(filepath.Join(t.TempDir(), "podplane.cluster.jsonc"), cfg, testClusterOptions())
	if err != nil {
		t.Fatalf("GenerateCluster returned error: %v", err)
	}
	got := fileContents(files)["podplane.cluster.main.tf"]
	if strings.Contains(got, `resource "podplane_netsy_seed_s3" "cluster"`) {
		t.Fatalf("generated cluster tf unexpectedly contains seed resource:\n%s", got)
	}
}

// TestGenerateAWSOIDCTerraform verifies the generated AWS OIDC Terraform
// contains the expected Easy OIDC settings.
func TestGenerateAWSOIDCTerraform(t *testing.T) {
	cfg := &oidcconfig.Config{OIDC: oidcconfig.OIDC{
		Provider:            oidcconfig.Provider{Kind: "aws", Region: "us-east-1", Account: "123456789012"},
		Hostname:            "https://auth.example.com",
		Domain:              oidcconfig.Domain{Zone: "example.com", Provider: oidcconfig.DomainProvider{Kind: "aws"}},
		Connector:           oidcconfig.Connector{Kind: "google", ClientSecretARN: "arn:connector"},
		SigningKeySecretARN: "arn:signing",
		DefaultRedirectURIs: []string{"http://localhost:8000"},
		Clients:             map[string]oidcconfig.Client{"kubelogin": {}},
	}}
	files, err := GenerateOIDC(cfg)
	if err != nil {
		t.Fatalf("GenerateOIDC returned error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3", len(files))
	}
	contents := fileContents(files)
	for _, name := range []string{
		"podplane.oidc.main.tf",
		"podplane.oidc.variables.tf",
		"podplane.oidc.outputs.tf",
	} {
		if _, ok := contents[name]; !ok {
			t.Fatalf("generated files missing %s: %#v", name, files)
		}
	}
	assertExpectedTerraform(t, "podplane.oidc.main.expected.tf", contents["podplane.oidc.main.tf"])
	assertExpectedTerraform(t, "podplane.oidc.variables.expected.tf", contents["podplane.oidc.variables.tf"])
	assertExpectedTerraform(t, "podplane.oidc.outputs.expected.tf", contents["podplane.oidc.outputs.tf"])
	got := contents["podplane.oidc.main.tf"] + contents["podplane.oidc.variables.tf"] + contents["podplane.oidc.outputs.tf"]
	for _, want := range []string{
		`oidc_addr = "auth.example.com"`,
		`connector_type = "google"`,
		`route53_zone_id = data.aws_route53_zone.oidc.zone_id`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated OIDC tf missing %q:\n%s", want, got)
		}
	}
}

// assertExpectedTerraform compares generated Terraform with a testdata file.
func assertExpectedTerraform(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_TFGEN_EXPECTED") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != got {
		t.Fatalf("%s mismatch\nwant:\n%s\ngot:\n%s", name, raw, got)
	}
}

// TestWriteFilesPreservesCustomTF verifies managed writes do not alter custom
// Terraform files.
func TestWriteFilesPreservesCustomTF(t *testing.T) {
	dir := t.TempDir()
	customPath := filepath.Join(dir, "custom.tf")
	if err := os.WriteFile(customPath, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(dir, []File{
		{Name: "podplane.cluster.main.tf", Content: "locals {}\n", Type: FileTypeTerraform},
		{Name: "podplane.cluster.vmconfig_knc_debian-13_arm64.json", Content: "{}\n", Type: FileTypeJSON},
	}); err != nil {
		t.Fatalf("WriteFiles returned error: %v", err)
	}
	custom, err := os.ReadFile(customPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(custom) != "custom" {
		t.Fatalf("custom file changed: %q", custom)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "podplane.cluster.vmconfig_knc_debian-13_arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}\n" {
		t.Fatalf("raw manifest = %q, want unmodified JSON", raw)
	}
}
