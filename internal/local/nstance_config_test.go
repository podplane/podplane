// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/deps"
	"github.com/podplane/podplane/internal/vm/qemu"
)

// TestLocalServiceAccountKeyPersistence checks new P-256 keys remain stable and
// existing RSA keys are preserved rather than silently rotated.
func TestLocalServiceAccountKeyPersistence(t *testing.T) {
	for _, existingRSA := range []bool{false, true} {
		name := "new-p256"
		if existingRSA {
			name = "existing-rsa"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, err := newLocalNstanceStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var original string
			if existingRSA {
				key, err := rsa.GenerateKey(rand.Reader, 2048)
				if err != nil {
					t.Fatal(err)
				}
				priv, err := x509.MarshalPKCS8PrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
				if err != nil {
					t.Fatal(err)
				}
				original = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}))
				if err := store.Put(ctx, "podplane/clusters/cluster-a/service-accounts.key", []byte(original)); err != nil {
					t.Fatal(err)
				}
				if err := store.Put(ctx, "podplane/clusters/cluster-a/service-accounts.pub", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})); err != nil {
					t.Fatal(err)
				}
			}
			pub, priv, err := localServiceAccountKeyPair(ctx, store, "cluster-a")
			if err != nil {
				t.Fatal(err)
			}
			if existingRSA {
				if priv != original {
					t.Fatal("replaced existing RSA key")
				}
			} else {
				block, _ := pem.Decode([]byte(priv))
				parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				key, ok := parsed.(*ecdsa.PrivateKey)
				if !ok || key.Curve != elliptic.P256() {
					t.Fatal("new signer must use ECDSA P-256")
				}
				block, _ = pem.Decode([]byte(pub))
				verificationKey, err := x509.ParsePKIXPublicKey(block.Bytes)
				if err != nil || !key.PublicKey.Equal(verificationKey) {
					t.Fatal("verification key does not match signer", err)
				}
			}
			pubAgain, privAgain, err := localServiceAccountKeyPair(ctx, store, "cluster-a")
			if err != nil || pubAgain != pub || privAgain != priv {
				t.Fatal("reloaded key pair changed", err)
			}
		})
	}
}

// TestConfigureLocalNstancePreparesBootstrap checks trust, nonce, runtime files, and unregistered instance state.
func TestConfigureLocalNstancePreparesBootstrap(t *testing.T) {
	dataDir := t.TempDir()
	bootstrap, err := configureLocalNstance(
		context.Background(),
		dataDir,
		"cluster-a",
		"knc123",
		"knc",
		"10.0.2.2:1234",
		"10.0.2.2:5678",
		"10.0.2.2",
		"OIDC_CA_CERT='test'\n",
	)
	if err != nil {
		t.Fatalf("configureLocalNstance: %v", err)
	}
	if bootstrap.CACert == "" {
		t.Fatal("expected CA cert")
	}
	if bootstrap.RegistrationNonceJWT == "" {
		t.Fatal("expected registration nonce JWT")
	}
	if bootstrap.ServerRegistrationAddr != "10.0.2.2:1234" {
		t.Fatalf("registration addr = %q", bootstrap.ServerRegistrationAddr)
	}
	if bootstrap.ServerAgentAddr != "10.0.2.2:5678" {
		t.Fatalf("agent addr = %q", bootstrap.ServerAgentAddr)
	}

	tenant := readFakeNstanceTenantState(t, dataDir, "cluster-a")
	files, ok := tenant["Files"].(map[string]any)
	if !ok {
		t.Fatalf("tenant files missing or invalid: %#v", tenant["Files"])
	}
	mutableEnv, ok := files["mutable.env"].(map[string]any)
	if !ok || mutableEnv["template"] != "OIDC_CA_CERT='test'\n" {
		t.Fatalf("tenant mutable.env = %#v", files["mutable.env"])
	}
	ingress, ok := files["ingress.server.crt"].(map[string]any)
	if !ok || ingress["kind"] != "certificate" || ingress["template"] != "ingress.server" {
		t.Fatalf("tenant ingress certificate = %#v", files["ingress.server.crt"])
	}
	key := ingress["key"].(map[string]any)
	if key["source"] != "agent" || key["name"] != "ingress.server" {
		t.Fatalf("ingress key = %v, want agent-owned ingress.server key", key)
	}
	// The v2 agent validates the entire patch before committing it. An empty
	// or malformed static key/certificate would block every runtime file.
	for name, value := range files {
		ext := filepath.Ext(name)
		file := value.(map[string]any)
		if file["kind"] != "string" || (ext != ".pub" && ext != ".key" && ext != ".crt") {
			continue
		}
		template, ok := file["template"].(string)
		if !ok {
			t.Fatalf("static file %s has no string content", name)
		}
		if block, _ := pem.Decode([]byte(template)); block == nil {
			t.Fatalf("static file %s is not valid PEM", name)
		}
	}

	instance := readFakeNstanceInstanceState(t, dataDir, "knc123")
	if instance.Hostname != "" || instance.IPv4 != "" || instance.IPv6 != "" || instance.Registered {
		t.Fatalf("new fake nstance instance identity = hostname %q ipv4 %q ipv6 %q registered %t", instance.Hostname, instance.IPv4, instance.IPv6, instance.Registered)
	}
}

// TestConfigureLocalNstancePreservesRegisteredInstanceIdentity checks reconfiguration preserves registered addresses and nonce.
func TestConfigureLocalNstancePreservesRegisteredInstanceIdentity(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	if _, err := configureLocalNstance(
		ctx,
		dataDir,
		"cluster-a",
		"knc123",
		"knc",
		"10.0.2.2:1234",
		"10.0.2.2:5678",
		"10.0.2.2",
		"OIDC_CA_CERT='test'\n",
	); err != nil {
		t.Fatalf("configureLocalNstance initial: %v", err)
	}

	instancePath := filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", "knc123", "instance.json")
	instanceData, err := os.ReadFile(instancePath)
	if err != nil {
		t.Fatalf("read fake nstance instance state: %v", err)
	}
	var instance fakeNstanceInstanceState
	if err := json.Unmarshal(instanceData, &instance); err != nil {
		t.Fatalf("decode fake nstance instance state: %v", err)
	}
	instance.Registered = true
	instance.Hostname = "knc123"
	instance.IPv4 = "10.0.2.15"
	instance.IPv6 = "fec0::5054:ff:fe12:3456"
	instance.NonceJWT = "registered-nonce"
	updated, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("marshal fake nstance instance state: %v", err)
	}
	if err := os.WriteFile(instancePath, updated, 0o600); err != nil {
		t.Fatalf("write fake nstance instance state: %v", err)
	}

	if _, err := configureLocalNstance(
		ctx,
		dataDir,
		"cluster-a",
		"knc123",
		"knc",
		"10.0.2.2:2234",
		"10.0.2.2:6678",
		"10.0.2.2",
		"OIDC_CA_CERT='updated'\n",
	); err != nil {
		t.Fatalf("configureLocalNstance existing: %v", err)
	}

	got := readFakeNstanceInstanceState(t, dataDir, "knc123")
	if got.Hostname != "knc123" || got.IPv4 != "10.0.2.15" || got.IPv6 != "fec0::5054:ff:fe12:3456" || !got.Registered || got.NonceJWT != "registered-nonce" {
		t.Fatalf("registered fake nstance instance state was overwritten: %#v", got)
	}
}

// fakeNstanceInstanceState decodes persisted instance identity and registration fields for assertions.
type fakeNstanceInstanceState struct {
	TenantID     string `json:"tenant_id"`
	InstanceID   string `json:"instance_id"`
	InstanceKind string `json:"instance_kind"`
	Hostname     string `json:"hostname"`
	IPv4         string `json:"ipv4"`
	IPv6         string `json:"ipv6"`
	NonceJWT     string `json:"nonce_jwt"`
	Registered   bool   `json:"registered"`
}

// readFakeNstanceInstanceState loads a fake server instance record, failing on read or decode errors.
func readFakeNstanceInstanceState(t *testing.T, dataDir, instanceID string) fakeNstanceInstanceState {
	t.Helper()
	instancePath := filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", instanceID, "instance.json")
	instanceData, err := os.ReadFile(instancePath)
	if err != nil {
		t.Fatalf("read fake nstance instance state: %v", err)
	}
	var instance fakeNstanceInstanceState
	if err := json.Unmarshal(instanceData, &instance); err != nil {
		t.Fatalf("decode fake nstance instance state: %v", err)
	}
	return instance
}

// readFakeNstanceTenantState loads a tenant's persisted runtime configuration for assertions.
func readFakeNstanceTenantState(t *testing.T, dataDir, tenantID string) map[string]any {
	t.Helper()
	tenantPath := filepath.Join(dataDir, "nstance-fake", "fakeserver", "tenants", tenantID, "runtime.json")
	tenantData, err := os.ReadFile(tenantPath)
	if err != nil {
		t.Fatalf("read fake nstance tenant state: %v", err)
	}
	var tenant map[string]any
	if err := json.Unmarshal(tenantData, &tenant); err != nil {
		t.Fatalf("decode fake nstance tenant state: %v", err)
	}
	return tenant
}

// TestPodplaneRuntimeConfigIncludesNodeSANs verifies API and ingress certificate addresses.
func TestPodplaneRuntimeConfigIncludesNodeSANs(t *testing.T) {
	cfg := podplaneRuntimeConfig("cluster-a", "cluster-a.k8s.localhost", []string{"198.18.0.1", "fdc6::1"}, nil)
	cert := cfg.Certificates["kube-apiserver.server"]
	for _, want := range []string{"cluster-a.k8s.localhost", "kube-apiserver.podplane.internal"} {
		if !slices.Contains(cert.DNS, want) {
			t.Fatalf("kube-apiserver.server DNS SANs = %v, want %s", cert.DNS, want)
		}
	}
	for _, want := range []string{"198.18.0.1", "fdc6::1"} {
		if !slices.Contains(cert.IP, want) {
			t.Fatalf("kube-apiserver.server IP SANs = %v, want %s", cert.IP, want)
		}
	}
	ingress := cfg.Certificates["ingress.server"]
	for _, want := range []string{"cluster-a.localhost", "*.cluster-a.localhost", "localhost", "{{ .Instance.Hostname }}"} {
		if !slices.Contains(ingress.DNS, want) {
			t.Fatalf("ingress.server DNS SANs = %v, want %s", ingress.DNS, want)
		}
	}
	for _, want := range []string{"127.0.0.1", "::1", "{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}"} {
		if !slices.Contains(ingress.IP, want) {
			t.Fatalf("ingress.server IP SANs = %v, want %s", ingress.IP, want)
		}
	}
}

// TestStartRefusesMixedNstanceMajorVersions checks only 2.x agents proceed past startup compatibility validation.
func TestStartRefusesMixedNstanceMajorVersions(t *testing.T) {
	for _, version := range []string{"", "1.3.4", "v1.3.4", "3.0.0", "2.0.0", "v2.0.0"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			cache := filepath.Join(dir, "deps")
			manager := deps.NewManager("", cache)
			manifest := deps.Manifest{VMConfig: deps.VMConfig{
				Dependencies: map[string]deps.Dependency{"nstance-agent": {Version: version}},
			}}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.WriteCachedManifest("knc", "arm64", raw); err != nil {
				t.Fatal(err)
			}
			local := &Local{
				dataDir: dir, runtimeDir: dir, clusterID: "demo", instanceKind: "knc", arch: "arm64", depsCacheDir: cache,
				vm: qemu.NewQemu(qemu.Options{DataDir: dir, RuntimeDir: dir, ClusterID: "demo", Arch: "arm64"}),
			}
			// Accepted versions proceed to the missing component cache, where the
			// callback stops startup without downloading or starting a guest.
			want := "requires a Nstance 2.x agent"
			if version == "2.0.0" || version == "v2.0.0" {
				want = "failed to download component image deps"
			}
			if _, err := local.Start(StartOptions{
				RunDownloadProgress: func(func(func(deps.DownloadEvent)) error) error { return os.ErrNotExist },
			}); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("mixed-version startup: %v", err)
			}
		})
	}
}
