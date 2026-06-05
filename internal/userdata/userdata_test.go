// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package userdata

import (
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
		Env: EnvVars{
			SSHAuthorizedKey:      "ssh-ed25519 AAAAexample",
			InstanceID:            "ins06djbn8xgdtz92astpmdv1jfk4",
			ClusterID:             "example-cluster",
			ProviderKind:          provider,
			ProviderRegion:        "local",
			ProviderZone:          "local",
			ProviderInstanceType:  "local",
			OIDCIssuer:            "http://10.0.2.2:1234/oidc",
			OIDCCAFile:            "/opt/crt/oidc-ca.pem",
			KubeLogLevel:          "5",
			KubeAPIPublicHostname: "localhost",
			KubeAPIEtcdServers:    "https://127.0.0.1:2378",
			TelemetryLogServices:  "first-boot-env,cron,ssh,netsy,nstance-agent,nstance-recv-watch,containerd,kube-apiserver,kube-controller-manager,kube-scheduler,kubelet,zot",
			RegistryHostname:      "registry.example.com",
		},
	}
	v.Env.SetObjectStorageEndpoint("http://10.0.2.2:1234/s3")
	v.Env.SetObjectStorageRegion("local")
	v.Env.SetObjectStorageCredentials("test", "test")
	return v
}

func TestRender_Local_HasDebianPasswordLine(t *testing.T) {
	v := baseVars("local")
	v.NstanceRegistrationNonceJWT = "nonce.jwt.value"
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
	// cluster-prefixed bucket names
	if !strings.Contains(out, "netsy=example-cluster-netsy") {
		t.Errorf("expected cluster-prefixed netsy bucket; got:\n%s", out)
	}
	if !strings.Contains(out, "registry=example-cluster-registry") {
		t.Errorf("expected cluster-prefixed registry bucket; got:\n%s", out)
	}
	if !strings.Contains(out, "telemetry=example-cluster-telemetry") {
		t.Errorf("expected cluster-prefixed telemetry bucket; got:\n%s", out)
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
	if !strings.Contains(out, "KUBE_API_PUBLIC_HOSTNAME='localhost'") {
		t.Errorf("expected KUBE_API_PUBLIC_HOSTNAME in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "KUBE_API_PORT='6443'") {
		t.Errorf("expected KUBE_API_PORT in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "NETSY_ENDPOINT='http://10.0.2.2:1234/s3'") {
		t.Errorf("expected NETSY_ENDPOINT in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "TELEMETRY_ACCESS_KEY_ID='test'") {
		t.Errorf("expected TELEMETRY_ACCESS_KEY_ID in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "TELEMETRY_LOG_SERVICES='first-boot-env,cron,ssh,netsy,nstance-agent,nstance-recv-watch,containerd,kube-apiserver,kube-controller-manager,kube-scheduler,kubelet,zot'") {
		t.Errorf("expected TELEMETRY_LOG_SERVICES in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "TELEMETRY_LOG_CLOUDINIT='true'") {
		t.Errorf("expected TELEMETRY_LOG_CLOUDINIT in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "REGISTRY_SECRET_ACCESS_KEY='test'") {
		t.Errorf("expected REGISTRY_SECRET_ACCESS_KEY in user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "REGISTRY_ENABLED='true'") {
		t.Errorf("expected REGISTRY_ENABLED default in user-data.env; got:\n%s", out)
	}
	if strings.Contains(out, "NSTANCE_REGISTRATION_NONCE_JWT=") {
		t.Errorf("did not expect nstance registration nonce to be written to user-data.env; got:\n%s", out)
	}
	if !strings.Contains(out, "cat > /opt/nstance-agent/identity/nonce.jwt <<'NSTANCE_NONCE_JWT'\nnonce.jwt.value\nNSTANCE_NONCE_JWT") {
		t.Errorf("expected nstance registration nonce to be written directly to nonce.jwt; got:\n%s", out)
	}
	if !strings.Contains(out, "VMCONFIG_ALREADY_INSTALLED=false") || !strings.Contains(out, "Skipping install.sh, configure.sh, and restart.sh because vmconfig is already installed.") {
		t.Errorf("expected installed vmconfig rerun guard; got:\n%s", out)
	}
	if !strings.Contains(out, "ensure_nstance_nonce_permissions") || !strings.Contains(out, "systemctl restart nstance-agent || true") {
		t.Errorf("expected installed vmconfig nonce refresh; got:\n%s", out)
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
	if !strings.Contains(out, "10.0.2.2 ${host}") {
		t.Errorf("expected local provider host mapping; got:\n%s", out)
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
	if !strings.Contains(out, "/opt/podplane/bin/restart.sh") {
		t.Errorf("expected aws user-data to run restart.sh directly; got:\n%s", out)
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
	v.Env.ProviderKind = ""
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
	v.Env.ClusterID = ""
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for missing ClusterID")
	}
}

func TestValidate_InvalidKubeAPIPort(t *testing.T) {
	v := baseVars("local")
	v.Env.KubeAPIPort = "not-a-port"
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for invalid KubeAPIPort")
	}
}

func TestValidate_InvalidTelemetryLogCloudinit(t *testing.T) {
	v := baseVars("local")
	v.Env.TelemetryLogCloudinit = "maybe"
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for invalid TelemetryLogCloudinit")
	}
}

func TestValidate_RegistryEnabledRequiresHostname(t *testing.T) {
	v := baseVars("local")
	v.Env.RegistryEnabled = "true"
	v.Env.RegistryHostname = ""
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error when registry is enabled without a hostname")
	}
}

func TestValidate_InvalidTelemetryLogServices(t *testing.T) {
	v := baseVars("local")
	v.Env.TelemetryLogServices = "kubelet,ssh.service"
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for invalid TelemetryLogServices")
	}
}

func TestValidate_RejectsUnsafeEnvValue(t *testing.T) {
	v := baseVars("local")
	v.Env.OIDCCustomCA = "line1\nline2"
	if _, err := v.Render(); err == nil {
		t.Fatalf("expected validation error for env value containing newline")
	}
}

func TestApplyDefaults_RespectsExplicitNames(t *testing.T) {
	v := baseVars("local")
	v.Env.NetsyBucket = "explicit-netsy"
	out, err := v.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "netsy=explicit-netsy") {
		t.Errorf("expected explicit netsy bucket to be preserved; got:\n%s", out)
	}
}

func TestObjectStorageSetters_SetComponentFields(t *testing.T) {
	v := &EnvVars{}
	v.SetObjectStorageEndpoint("https://object.example")
	v.SetObjectStorageRegion("region-1")
	v.SetObjectStorageCredentials("access", "secret")

	if v.NetsyEndpoint != "https://object.example" || v.TelemetryEndpoint != "https://object.example" || v.RegistryEndpoint != "https://object.example" {
		t.Fatalf("expected shared object storage endpoint to be set on all components: %#v", v)
	}
	if v.NetsyRegion != "region-1" || v.TelemetryRegion != "region-1" || v.RegistryRegion != "region-1" {
		t.Fatalf("expected shared object storage region to be set on all components: %#v", v)
	}
	if v.NetsyAccessKeyID != "access" || v.TelemetryAccessKeyID != "access" || v.RegistryAccessKeyID != "access" {
		t.Fatalf("expected shared object storage access key ID to be set on all components: %#v", v)
	}
	if v.NetsySecretAccessKey != "secret" || v.TelemetrySecretAccessKey != "secret" || v.RegistrySecretAccessKey != "secret" {
		t.Fatalf("expected shared object storage secret access key to be set on all components: %#v", v)
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
