// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/nstance-dev/nstance/pkg/fakeserver"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/clusterspec"
)

type nstanceAgentUserData struct {
	CACert                 string
	RegistrationNonceJWT   string
	ServerRegistrationAddr string
	ServerAgentAddr        string
}

// configureLocalNstance prepares the fake Nstance state needed by one local VM.
// It writes tenant config, ensures stable kubernetes service-account keys exist,
// prepares an instance nonce, and returns the env vars that user-data needs to
// hand to nstance-agent.
func configureLocalNstance(ctx context.Context, dataDir, clusterID, instanceID, instanceKind, registrationAddr, agentAddr, advertiseHost, mutableEnv string) (nstanceAgentUserData, error) {
	store, err := newLocalNstanceStore(filepath.Join(dataDir, "nstance-fake"))
	if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("initialize fake nstance store: %w", err)
	}
	server, err := fakeserver.New(fakeserver.Config{
		Store:         store,
		ClusterID:     "podplane-local",
		ShardID:       "local",
		AdvertiseHost: advertiseHost,
	})
	if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("initialize fake nstance server state: %w", err)
	}
	serviceAccountPub, serviceAccountKey, err := localServiceAccountKeyPair(ctx, store, clusterID)
	if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("prepare local service account keys: %w", err)
	}

	// Fetch the fake Nstance CA before configuring the tenant so the tenant's
	// runtime files (which include ca.crt) match what the agent will receive.
	// This keeps the runtime config hash that fakeserver embeds in the
	// registration nonce consistent with the files actually delivered.
	nstanceCACertPEM, err := server.CACert(ctx)
	if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("load fake nstance CA certificate: %w", err)
	}
	nstanceCACert := base64.StdEncoding.EncodeToString(nstanceCACertPEM)

	// Include the default Kubernetes API Service IPs in the API server certificate.
	serviceNetwork, err := clusterconfig.ServiceNetworkFromCIDRs(nil)
	if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("derive local service network: %w", err)
	}
	apiServiceIPs := make([]string, len(serviceNetwork.API))
	for i, ip := range serviceNetwork.API {
		apiServiceIPs[i] = ip.String()
	}

	// Local maps each Podplane cluster to one fake Nstance tenant. Static files
	// here use normal Nstance string-file semantics. Configure the tenant
	// first so ConfigureInstance can embed the tenant's runtime config hash in
	// the registration nonce, matching real Nstance server behavior.
	tenantConfig := podplaneRuntimeConfig(clusterID, LocalKubernetesAPIHostname(clusterID), apiServiceIPs, map[string]string{
		"mutable.env":          mutableEnv,
		"ca.pub":               "",
		"ca.crt":               string(nstanceCACertPEM),
		"service-accounts.pub": serviceAccountPub,
		"service-accounts.key": serviceAccountKey,
	})
	tenantConfig.TenantID = clusterID
	if err := server.ConfigureTenant(ctx, tenantConfig); err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("configure fake nstance tenant: %w", err)
	}

	// ConfigureInstance persists the fake Nstance instance and registration nonce.
	// Only do this before the first registration. After an existing local VM has
	// registered, fake Nstance has authoritative hostname/IP values reported by
	// nstance-agent; overwriting the instance on a later `local start` would erase
	// those dynamic values even though the VM will not re-register.
	// InstanceEnvWithAddrs renders agent env vars using addresses published
	// by the already-running background local server.
	instanceKey := filepath.ToSlash(filepath.Join("fakeserver", "instances", instanceID, "instance.json"))
	if _, err := store.Get(ctx, instanceKey); errors.Is(err, fakeserver.ErrNotFound) {
		if err := server.ConfigureInstance(ctx, fakeserver.InstanceRequest{
			TenantID:     clusterID,
			InstanceID:   instanceID,
			InstanceKind: instanceKind,
		}); err != nil {
			return nstanceAgentUserData{}, fmt.Errorf("configure fake nstance instance: %w", err)
		}
	} else if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("load fake nstance instance state: %w", err)
	}
	instanceEnv, err := server.InstanceEnvWithAddrs(ctx, instanceID, fakeserver.ServerAddrs{
		RegistrationAddr: registrationAddr,
		AgentAddr:        agentAddr,
	})
	if err != nil {
		return nstanceAgentUserData{}, fmt.Errorf("render fake nstance instance env: %w", err)
	}
	nstanceNonceJWT := instanceEnv.Vars["NSTANCE_REGISTRATION_NONCE_JWT"]

	return nstanceAgentUserData{
		CACert:                 nstanceCACert,
		RegistrationNonceJWT:   nstanceNonceJWT,
		ServerRegistrationAddr: registrationAddr,
		ServerAgentAddr:        agentAddr,
	}, nil
}

// localServiceAccountKeyPair returns the stable Kubernetes service-account key
// pair for a local cluster, creating and storing it on first use.
func localServiceAccountKeyPair(ctx context.Context, store fakeserver.Store, clusterID string) (publicPEM, privatePEM string, err error) {
	pubKey := filepath.ToSlash(filepath.Join("podplane", "clusters", clusterID, "service-accounts.pub"))
	privKey := filepath.ToSlash(filepath.Join("podplane", "clusters", clusterID, "service-accounts.key"))
	pub, pubErr := store.Get(ctx, pubKey)
	priv, privErr := store.Get(ctx, privKey)
	if pubErr == nil && privErr == nil {
		if serviceAccountPrivateKeySupported(priv) {
			return string(pub), string(priv), nil
		}
	}
	if pubErr != nil && !errors.Is(pubErr, fakeserver.ErrNotFound) {
		return "", "", pubErr
	}
	if privErr != nil && !errors.Is(privErr, fakeserver.ErrNotFound) {
		return "", "", privErr
	}

	publicPEM, privatePEM, err = serviceAccountKeyPairPEM()
	if err != nil {
		return "", "", err
	}
	if err := store.Put(ctx, pubKey, []byte(publicPEM)); err != nil {
		return "", "", err
	}
	if err := store.Put(ctx, privKey, []byte(privatePEM)); err != nil {
		return "", "", err
	}
	return publicPEM, privatePEM, nil
}

// serviceAccountPrivateKeySupported reports whether Kubernetes can use the
// stored private key for service-account token signing.
func serviceAccountPrivateKeySupported(privatePEM []byte) bool {
	block, _ := pem.Decode(privatePEM)
	if block == nil {
		return false
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return false
	}
	_, ok := key.(*rsa.PrivateKey)
	return ok
}

// serviceAccountKeyPairPEM generates a PEM-encoded RSA keypair suitable for
// Kubernetes service-account signing in local clusters.
func serviceAccountKeyPairPEM() (publicPEM, privatePEM string, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", "", err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	if public == nil || private == nil {
		return "", "", fmt.Errorf("failed to encode service account keys")
	}
	return string(public), string(private), nil
}

// podplaneRuntimeConfig builds the Nstance runtime config for Podplane's local
// VM profile, including the certificate files that nstance-agent must request
// and the static files that nstance-agent should receive.
func podplaneRuntimeConfig(clusterID, apiHostname string, apiServiceIPs []string, staticFiles map[string]string) fakeserver.TenantConfig {
	files := map[string]fakeserver.FileConfig{}
	for name, content := range staticFiles {
		files[name] = fakeserver.FileConfig{Kind: "string", Template: content}
	}
	certs := map[string]fakeserver.CertificateConfig{}
	for _, cert := range clusterspec.Certificates("{{ .Vars.NetsyClusterID }}", apiHostname, apiServiceIPs) {
		certCN := cert.CN
		certs[cert.Name] = fakeserver.CertificateConfig{
			Kind:         cert.Kind,
			CN:           &certCN,
			Organization: cert.Organization,
			DNS:          cert.DNS,
			IP:           cert.IP,
			URI:          cert.URI,
			TTL:          cert.TTL,
		}
	}
	for _, name := range clusterspec.CertificateFiles("knc") {
		files[name+".crt"] = fakeserver.FileConfig{
			Kind:     "certificate",
			Template: name,
			Key:      &fakeserver.KeyConfig{Source: "agent", Name: name},
		}
	}
	return fakeserver.TenantConfig{
		Kind:         "knc",
		Arch:         "arm64",
		InstanceType: "local",
		Vars: map[string]string{
			"NetsyClusterID": clusterID,
		},
		Files:        files,
		Certificates: certs,
	}
}
