// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"path/filepath"
	"testing"
	"time"
)

// TestLocalServerCertificateCoversHosts checks certificates cover ingress and Kubernetes API names.
func TestLocalServerCertificateCoversHosts(t *testing.T) {
	certFile := writeTestCertificate(t, []string{"dev.localhost", "*.dev.localhost", "dev.k8s.localhost"})

	if !localServerCertificateCoversHosts(certFile, "dev.localhost", "*.dev.localhost", "dev.k8s.localhost") {
		t.Fatalf("expected certificate to cover local ingress and Kubernetes API hosts")
	}
}

// TestLocalServerCertificateCoversHostsRejectsMissingHost checks a missing API hostname invalidates coverage.
func TestLocalServerCertificateCoversHostsRejectsMissingHost(t *testing.T) {
	certFile := writeTestCertificate(t, []string{"dev.localhost", "*.dev.localhost"})

	if localServerCertificateCoversHosts(certFile, "dev.localhost", "*.dev.localhost", "dev.k8s.localhost") {
		t.Fatalf("expected certificate missing Kubernetes API host to be rejected")
	}
}

// testServerCertificate generates a self-signed RSA server certificate for the supplied DNS names.
func testServerCertificate(t *testing.T, dnsNames []string) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "podplane-local-ingress-test",
		},
		DNSNames:              dnsNames,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key}
}

// writeTestCertificate writes a temporary self-signed RSA server certificate for the supplied DNS names.
func writeTestCertificate(t *testing.T, dnsNames []string) string {
	t.Helper()
	cert := testServerCertificate(t, dnsNames)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	if err := writePEMFile(certFile, "CERTIFICATE", cert.Certificate[0], 0o644); err != nil {
		t.Fatalf("writePEMFile: %v", err)
	}
	return certFile
}
