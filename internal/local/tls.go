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
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// localIngressTLSConfig builds the TLS config used by the shared local ingress
// proxy to select mkcert certificates dynamically from the requested hostname.
func localIngressTLSConfig(dataDir string) *tls.Config {
	var mu sync.Mutex
	certs := map[string]*tls.Certificate{}
	var defaultCert *tls.Certificate
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			clusterID, err := localIngressClusterID(hello.ServerName)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if defaultCert != nil {
					return defaultCert, nil
				}
				certFile, keyFile, err := ensureLocalIngressCertificate(dataDir, "_default", "localhost")
				if err != nil {
					return nil, err
				}
				cert, err := tls.LoadX509KeyPair(certFile, keyFile)
				if err != nil {
					return nil, fmt.Errorf("load default local ingress TLS certificate: %w", err)
				}
				defaultCert = &cert
				return &cert, nil
			}

			if cert := certs[clusterID]; cert != nil {
				return cert, nil
			}

			zone := clusterID + ".localhost"
			certFile, keyFile, err := ensureLocalIngressCertificate(dataDir, clusterID, zone, "*."+zone, LocalKubernetesAPIHostname(clusterID))
			if err != nil {
				return nil, err
			}
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("load local ingress TLS certificate for %s: %w", clusterID, err)
			}
			certs[clusterID] = &cert
			return &cert, nil
		},
	}
}

// ensureLocalIngressCertificate returns an mkcert certificate for the given
// local ingress hostnames, creating one if it does not already exist.
func ensureLocalIngressCertificate(dataDir, name string, hosts ...string) (string, string, error) {
	if len(hosts) == 0 {
		return "", "", fmt.Errorf("local ingress TLS certificate requires at least one hostname")
	}
	certDir := filepath.Join(dataDir, "local-server", "tls", name)
	certFile := filepath.Join(certDir, "cert.pem")
	keyFile := filepath.Join(certDir, "key.pem")
	if _, certErr := os.Stat(certFile); certErr == nil {
		if _, keyErr := os.Stat(keyFile); keyErr == nil {
			if localServerCertificateCoversHosts(certFile, hosts...) {
				return certFile, keyFile, nil
			}
		}
	}
	if _, err := exec.LookPath("mkcert"); err != nil {
		return "", "", fmt.Errorf("mkcert is required for local ingress TLS; install mkcert and run 'mkcert -install': %w", err)
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create local ingress TLS directory: %w", err)
	}
	_ = os.Remove(certFile)
	_ = os.Remove(keyFile)
	args := append([]string{"-cert-file", certFile, "-key-file", keyFile}, hosts...)
	cmd := exec.Command("mkcert", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("generate local ingress TLS certificate with mkcert: %w\n%s", err, string(output))
	}
	return certFile, keyFile, nil
}

// localServerCertificateCoversHosts reports whether an existing certificate can
// be reused for every requested local server hostname.
func localServerCertificateCoversHosts(certFile string, hosts ...string) bool {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	for _, host := range hosts {
		verifyHost := host
		if suffix, ok := strings.CutPrefix(host, "*."); ok {
			verifyHost = "wildcard-test." + suffix
		}
		if err := cert.VerifyHostname(verifyHost); err != nil {
			return false
		}
	}
	return true
}

// ensureLocalOIDCCertificate returns a local-server-owned CA certificate and a
// server certificate for the fake HTTPS OIDC issuer, creating them if needed.
func ensureLocalOIDCCertificate(dataDir string, hosts ...string) (caCertFile, certFile, keyFile string, err error) {
	if len(hosts) == 0 {
		return "", "", "", fmt.Errorf("local OIDC TLS certificate requires at least one hostname or IP")
	}
	certDir := filepath.Join(dataDir, "local-server", "tls", "oidc")
	caCertFile = filepath.Join(certDir, "ca.pem")
	caKeyFile := filepath.Join(certDir, "ca-key.pem")
	certFile = filepath.Join(certDir, "cert.pem")
	keyFile = filepath.Join(certDir, "key.pem")
	if _, certErr := os.Stat(caCertFile); certErr == nil {
		if _, keyErr := os.Stat(caKeyFile); keyErr == nil {
			if _, certErr := os.Stat(certFile); certErr == nil {
				if _, keyErr := os.Stat(keyFile); keyErr == nil {
					if localServerCertificateCoversHosts(certFile, hosts...) {
						return caCertFile, certFile, keyFile, nil
					}
				}
			}
		}
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("create local OIDC TLS directory: %w", err)
	}

	caKey, caCert, err := loadOrCreateLocalOIDCCA(caCertFile, caKeyFile)
	if err != nil {
		return "", "", "", err
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", fmt.Errorf("generate local OIDC server key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", "", fmt.Errorf("generate local OIDC server certificate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "podplane-local-oidc",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return "", "", "", fmt.Errorf("generate local OIDC server certificate: %w", err)
	}
	if err := writePEMFile(certFile, "CERTIFICATE", certDER, 0o644); err != nil {
		return "", "", "", err
	}
	if err := writePEMFile(keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey), 0o600); err != nil {
		return "", "", "", err
	}
	return caCertFile, certFile, keyFile, nil
}

func loadOrCreateLocalOIDCCA(certFile, keyFile string) (*rsa.PrivateKey, *x509.Certificate, error) {
	certPEM, certErr := os.ReadFile(certFile)
	keyPEM, keyErr := os.ReadFile(keyFile)
	if certErr == nil && keyErr == nil {
		certBlock, _ := pem.Decode(certPEM)
		keyBlock, _ := pem.Decode(keyPEM)
		if certBlock != nil && keyBlock != nil {
			cert, err := x509.ParseCertificate(certBlock.Bytes)
			if err == nil {
				key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
				if err == nil {
					return key, cert, nil
				}
			}
		}
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate local OIDC CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate local OIDC CA certificate serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "podplane-local-oidc-ca",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("generate local OIDC CA certificate: %w", err)
	}
	if err := writePEMFile(certFile, "CERTIFICATE", certDER, 0o644); err != nil {
		return nil, nil, err
	}
	if err := writePEMFile(keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key), 0o600); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated local OIDC CA certificate: %w", err)
	}
	return key, cert, nil
}

func writePEMFile(path, blockType string, der []byte, perm os.FileMode) error {
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if pemBytes == nil {
		return fmt.Errorf("encode %s PEM", path)
	}
	if err := os.WriteFile(path, pemBytes, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
