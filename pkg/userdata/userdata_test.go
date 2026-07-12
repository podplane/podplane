// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package userdata

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/deps"
)

func sampleManifest() *deps.Manifest {
	return &deps.Manifest{
		VMConfig: deps.VMConfig{
			Version: "2026.01.01",
			Kind:    "knc",
			OS: deps.OSInfo{
				Name: "debian-13",
				Arch: "arm64",
				Image: deps.Dependency{
					Version: "20260101",
					URL:     "https://example/img/debian-13.qcow2",
					Type:    "image",
					Digest:  "sha256:" + strings.Repeat("a", 64),
					Cached:  true,
				},
			},
			Dependencies: map[string]deps.Dependency{
				"runc": {
					Version: "1.2.3",
					URL:     "https://example/deps/runc",
					Type:    "binary",
					Digest:  "sha256:" + strings.Repeat("b", 64),
					Cached:  true,
				},
				"vmconfig": {
					Version: "2026.01.01",
					URL:     "https://example/deps/vmconfig/vmconfig_knc_arm64.tar.gz",
					Type:    "tar.gz",
					Digest:  "sha256:" + strings.Repeat("c", 64),
					Cached:  true,
				},
			},
		},
	}
}

func baseVars(provider string) *TemplateVars {
	v := &TemplateVars{
		Manifest:      sampleManifest(),
		DepsMirrorURL: "http://10.0.2.2:1234/deps",
		Cluster:       ClusterData{ID: "example-cluster"},
		Provider: ProviderData{
			Kind:   provider,
			Region: "local",
			Zone:   "local",
		},
		Instance: InstanceData{
			ID:   "ins06djbn8xgdtz92astpmdv1jfk4",
			Type: "local",
		},
	}
	v.ImmutableSSHAuthorizedKeys = "ssh-ed25519 AAAAexample"
	v.EnableSSM = provider == "aws"
	return v
}

func TestRender_Local_HasDebianPasswordLine(t *testing.T) {
	v := baseVars("local")
	v.Nonce = "nonce.jwt.value"
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, `echo "debian:devonly" | chpasswd`) {
		t.Errorf("expected debian password line for local provider; got:\n%s", out)
	}
	// hostname set
	if !strings.Contains(out, "hostnamectl set-hostname ins06djbn8xgdtz92astpmdv1jfk4") {
		t.Errorf("expected hostnamectl line with InstanceID; got:\n%s", out)
	}
	// manifest items rendered (os-image excluded — VM is already running on it)
	if strings.Contains(out, "os-image") {
		t.Errorf("os-image should not appear in install items; got:\n%s", out)
	}
	// dep download line
	if !strings.Contains(out, "Downloading 2 dependencies") {
		t.Errorf("expected download line; got:\n%s", out)
	}
	// dep verification line
	if !strings.Contains(out, "sha256:"+strings.Repeat("b", 64)+"  ${ARTIFACTS_DIR}/runc") {
		t.Errorf("expected runc checksum verification line; got:\n%s", out)
	}
	if !strings.Contains(out, "sha512:*) echo") {
		t.Errorf("expected sha512 checksum verification support; got:\n%s", out)
	}
	// user-data.env written
	if !strings.Contains(out, "cat > /opt/podplane/etc/user-data.env") {
		t.Errorf("expected user-data.env to be written under /opt/podplane/etc; got:\n%s", out)
	}
	if !strings.Contains(out, "CLUSTER_ID='example-cluster'") {
		t.Errorf("expected CLUSTER_ID in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "PROVIDER_KIND='local'") {
		t.Errorf("expected PROVIDER_KIND in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "IMMUTABLE_SSH_AUTHORIZED_KEYS='ssh-ed25519 AAAAexample'") {
		t.Errorf("expected immutable SSH key in user-data.env; got:\n%s", out)
	}
	if strings.Index(out, "IMMUTABLE_SSH_AUTHORIZED_KEYS='ssh-ed25519 AAAAexample'") > strings.Index(out, "Downloading 2 dependencies") {
		t.Errorf("expected immutable SSH key installation before dependency downloads; got:\n%s", out)
	}
	if strings.Contains(out, "TELEMETRY_S3_ACCESS_KEY_ID=") || strings.Contains(out, "TELEMETRY_LOG_SERVICES=") || strings.Contains(out, "TELEMETRY_LOG_CLOUDINIT=") {
		t.Errorf("did not expect telemetry vars in user-data.env; got:\n%s", out)
	}
	if strings.Contains(out, "OIDC_ISSUER='") || strings.Contains(out, "KUBE_API_PUBLIC_HOSTNAME='") || strings.Contains(out, "KUBE_API_PORT='") || strings.Contains(out, "NETSY_ENDPOINT='") || strings.Contains(out, "REGISTRY_SECRET_ACCESS_KEY='") || strings.Contains(out, "REGISTRY_ENABLED='") {
		t.Errorf("did not expect oidc/kube/netsy/registry config in user-data.env; got:\n%s", out)
	}
	if strings.Contains(out, "NSTANCE_REGISTRATION_NONCE_JWT=") {
		t.Errorf("did not expect nstance registration nonce to be written to user-data.env; got:\n%s", out)
	}
	if strings.Contains(out, "amazon-ssm-agent") {
		t.Errorf("did not expect local user-data to include AWS SSM bootstrap; got:\n%s", out)
	}
	if strings.Contains(out, "<no value>") {
		t.Errorf("did not expect missing template vars to render as <no value>; got:\n%s", out)
	}
	if !strings.Contains(out, "cat > /opt/nstance-agent/identity/nonce.jwt <<'NSTANCE_NONCE_JWT'\nnonce.jwt.value\nNSTANCE_NONCE_JWT") {
		t.Errorf("expected nstance registration nonce to be written directly to nonce.jwt; got:\n%s", out)
	}
	// install.sh and configure.sh invoked
	if !strings.Contains(out, "/opt/podplane/bin/install.sh") {
		t.Errorf("expected install.sh invocation; got:\n%s", out)
	}
	if !strings.Contains(out, "/opt/podplane/bin/configure.sh") {
		t.Errorf("expected configure.sh invocation; got:\n%s", out)
	}
	if strings.Contains(out, "/opt/podplane/bin/configure.sh false") {
		t.Errorf("did not expect local user-data to pass restart-suppression arg to configure.sh; got:\n%s", out)
	}
	if !strings.Contains(out, "# --- Local provider VM preparation") {
		t.Errorf("expected local provider preparation section; got:\n%s", out)
	}
	if !strings.Contains(out, "127.0.0.1 oidc.localhost") {
		t.Errorf("expected local provider OIDC host mapping; got:\n%s", out)
	}
	if strings.Contains(out, "10.0.2.2 ${host}") {
		t.Errorf("did not expect generic local provider host mapping; got:\n%s", out)
	}
	if !strings.Contains(out, "/opt/podplane/bin/restart.sh") {
		t.Errorf("expected local user-data to restart services explicitly; got:\n%s", out)
	}
}

func TestRender_AWS_NoDebianPasswordLine(t *testing.T) {
	v := baseVars("aws")
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, `echo "debian:devonly" | chpasswd`) {
		t.Errorf("did not expect debian password line for aws provider; got:\n%s", out)
	}
	if strings.Contains(out, "Local provider VM preparation") {
		t.Errorf("did not expect local provider preparation for aws provider; got:\n%s", out)
	}
	if strings.Contains(out, "/opt/podplane/bin/configure.sh false") {
		t.Errorf("did not expect aws user-data to suppress service restarts; got:\n%s", out)
	}
	if !strings.Contains(out, "Ensuring AWS SSM Agent is installed and running") {
		t.Errorf("expected aws user-data to install SSM agent; got:\n%s", out)
	}
	if !strings.Contains(out, "amazon-ssm-agent.deb") {
		t.Errorf("expected aws user-data to download SSM agent deb; got:\n%s", out)
	}
	if !strings.Contains(out, "Checking connectivity to nstance-server") {
		t.Errorf("expected aws user-data to check nstance-server connectivity; got:\n%s", out)
	}
	if !strings.Contains(out, "/dev/tcp/${REGISTRATION_ADDR%:*}/${REGISTRATION_ADDR##*:}") {
		t.Errorf("expected aws user-data to use TCP connectivity check; got:\n%s", out)
	}
	if !strings.Contains(out, "/opt/podplane/bin/restart.sh") {
		t.Errorf("expected aws user-data to run restart.sh directly; got:\n%s", out)
	}
}

// TestRenderManifest verifies rendering from a pinned manifest document.
func TestRenderManifest(t *testing.T) {
	raw, err := json.Marshal(sampleManifest())
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(raw, Options{
		DepsMirrorURL:              "https://deps.podplane.dev",
		ProviderKind:               "aws",
		AWSAccountID:               "${local.aws_account_id}",
		ImmutableSSHAuthorizedKeys: "${var.immutable_ssh_authorized_keys}",
		EnableSSM:                  true,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"# Provider: aws",
		"AWS_ACCOUNT_ID='${local.aws_account_id}'",
		"IMMUTABLE_SSH_AUTHORIZED_KEYS='${var.immutable_ssh_authorized_keys}'",
		"https://deps.podplane.dev/vmconfig/artifacts/runc/1.2.3/runc",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered userdata missing %q:\n%s", want, out)
		}
	}
}

// TestRenderRejectsInvalidManifest verifies malformed and
// incomplete manifests are rejected before rendering.
func TestRenderRejectsInvalidManifest(t *testing.T) {
	if _, err := Render([]byte("{"), Options{ProviderKind: "aws"}); err == nil {
		t.Fatal("expected invalid manifest error")
	}
	if _, err := Render([]byte(`{"vmconfig":{}}`), Options{ProviderKind: "aws"}); err == nil {
		t.Fatal("expected incomplete manifest error")
	}
}

func TestRender_NoDepsMirrorURL_UsesOriginalURLs(t *testing.T) {
	v := baseVars("aws")
	v.DepsMirrorURL = "" // production: no mirror
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Should contain the original upstream URL from the manifest.
	if !strings.Contains(out, "https://example/deps/runc") {
		t.Errorf("expected original upstream URL when DepsMirrorURL is empty; got:\n%s", out)
	}
	// Should NOT contain a constructed mirror-style URL.
	if strings.Contains(out, "/runc/1.2.3/runc") {
		t.Errorf("expected no mirror-style URL path when DepsMirrorURL is empty; got:\n%s", out)
	}
}

func TestRender_WithDepsMirrorURL_UsesMirrorURLs(t *testing.T) {
	v := baseVars("local")
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Should contain the constructed proxy URL.
	if !strings.Contains(out, "http://10.0.2.2:1234/deps/vmconfig/artifacts/runc/1.2.3/runc") {
		t.Errorf("expected mirror-style URL when DepsMirrorURL is set; got:\n%s", out)
	}
	// Should NOT contain the original upstream URL.
	if strings.Contains(out, "https://example/deps/runc") {
		t.Errorf("expected no original URL when DepsMirrorURL is set; got:\n%s", out)
	}
}

func TestValidate_MissingProvider(t *testing.T) {
	v := baseVars("local")
	v.Provider.Kind = ""
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for missing ProviderKind")
	}
}

func TestValidate_InvalidProvider(t *testing.T) {
	v := baseVars("notreal")
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for unknown ProviderKind")
	}
}

func TestValidate_MissingManifest(t *testing.T) {
	v := baseVars("local")
	v.Manifest = nil
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for missing Manifest")
	}
}

func TestValidate_MissingClusterID(t *testing.T) {
	v := baseVars("local")
	v.Cluster.ID = ""
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for missing ClusterID")
	}
}

func TestRender_DevManifest_WaitsForSyncedVMConfigBeforeScripts(t *testing.T) {
	v := baseVars("local")
	// Simulate --vmconfig dev manifest: vmconfig has no URL/digest.
	v.Manifest.VMConfig.Dependencies["vmconfig"] = deps.Dependency{}
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "Extracting vmconfig") {
		t.Errorf("dev manifest should not extract vmconfig; got:\n%s", out)
	}
	if !strings.Contains(out, "No vmconfig package was included in the vmconfig manifest.") {
		t.Errorf("expected dev manifest sync guidance; got:\n%s", out)
	}
	if !strings.Contains(out, "apt-get install -y --no-install-recommends rsync") {
		t.Errorf("expected local dev manifest to install rsync before sync guidance; got:\n%s", out)
	}
	if !strings.Contains(out, "if [ ! -x /opt/podplane/bin/install.sh ]; then") {
		t.Errorf("expected dev manifest to wait for synced install.sh; got:\n%s", out)
	}
	if !strings.Contains(out, "Running install.sh") {
		t.Errorf("expected dev manifest to run install.sh after sync; got:\n%s", out)
	}
	if !strings.Contains(out, "Running configure.sh") {
		t.Errorf("expected dev manifest to run configure.sh after sync; got:\n%s", out)
	}
	if !strings.Contains(out, "Running restart.sh") {
		t.Errorf("expected dev manifest to run restart.sh after sync; got:\n%s", out)
	}
	// Other deps should still be downloaded.
	if !strings.Contains(out, "Downloading 1 dependencies") {
		t.Errorf("expected download line; got:\n%s", out)
	}
}

func TestRender_DevManifest_RsyncBootstrapIsLocalOnly(t *testing.T) {
	v := baseVars("aws")
	// Simulate --vmconfig dev manifest: vmconfig has no URL/digest.
	v.Manifest.VMConfig.Dependencies["vmconfig"] = deps.Dependency{}
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "apt-get install -y --no-install-recommends rsync") {
		t.Errorf("did not expect non-local dev manifest to install rsync; got:\n%s", out)
	}

	v = baseVars("local")
	out, err = v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "apt-get install -y --no-install-recommends rsync") {
		t.Errorf("did not expect packaged local manifest to install rsync; got:\n%s", out)
	}
}
