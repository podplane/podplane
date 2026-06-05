// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

	instance := readFakeNstanceInstanceState(t, dataDir, "knc123")
	if instance.Hostname != "" || instance.IPv4 != "" || instance.IPv6 != "" || instance.Registered {
		t.Fatalf("new fake nstance instance identity = hostname %q ipv4 %q ipv6 %q registered %t", instance.Hostname, instance.IPv4, instance.IPv6, instance.Registered)
	}
}

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
	); err != nil {
		t.Fatalf("configureLocalNstance existing: %v", err)
	}

	got := readFakeNstanceInstanceState(t, dataDir, "knc123")
	if got.Hostname != "knc123" || got.IPv4 != "10.0.2.15" || got.IPv6 != "fec0::5054:ff:fe12:3456" || !got.Registered || got.NonceJWT != "registered-nonce" {
		t.Fatalf("registered fake nstance instance state was overwritten: %#v", got)
	}
}

func TestConfigureLocalNstanceRefreshesUnregisteredInstanceNonce(t *testing.T) {
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
	); err != nil {
		t.Fatalf("configureLocalNstance initial: %v", err)
	}

	instancePath := filepath.Join(dataDir, "nstance-fake", "fakeserver", "instances", "knc123", "instance.json")
	instance := readFakeNstanceInstanceState(t, dataDir, "knc123")
	instance.Hostname = "stale-hostname"
	instance.IPv4 = "10.0.2.15"
	instance.IPv6 = "fec0::5054:ff:fe12:3456"
	instance.NonceJWT = "stale-nonce"
	updated, err := json.Marshal(instance)
	if err != nil {
		t.Fatalf("marshal fake nstance instance state: %v", err)
	}
	if err := os.WriteFile(instancePath, updated, 0o600); err != nil {
		t.Fatalf("write fake nstance instance state: %v", err)
	}

	bootstrap, err := configureLocalNstance(
		ctx,
		dataDir,
		"cluster-a",
		"knc123",
		"knc",
		"10.0.2.2:2234",
		"10.0.2.2:6678",
		"10.0.2.2",
	)
	if err != nil {
		t.Fatalf("configureLocalNstance existing: %v", err)
	}

	got := readFakeNstanceInstanceState(t, dataDir, "knc123")
	if got.Registered {
		t.Fatalf("unregistered fake nstance instance became registered: %#v", got)
	}
	if got.Hostname != "" || got.IPv4 != "" || got.IPv6 != "" {
		t.Fatalf("unregistered fake nstance instance identity was preserved: %#v", got)
	}
	if got.NonceJWT == "" || got.NonceJWT == "stale-nonce" {
		t.Fatalf("unregistered fake nstance instance nonce was not refreshed: %#v", got)
	}
	if bootstrap.RegistrationNonceJWT != got.NonceJWT {
		t.Fatalf("bootstrap nonce = %q, want refreshed instance nonce %q", bootstrap.RegistrationNonceJWT, got.NonceJWT)
	}
}

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

func TestPodplaneRuntimeConfigIncludesInternalKubeAPISAN(t *testing.T) {
	cfg := podplaneRuntimeConfig("cluster-a", nil)
	cert := cfg.Certificates["kube-apiserver.server"]
	for _, name := range cert.DNS {
		if name == "kube-apiserver.podplane.internal" {
			return
		}
	}
	t.Fatalf("kube-apiserver.server DNS SANs = %v, want kube-apiserver.podplane.internal", cert.DNS)
}
