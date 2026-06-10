// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/vm/qemu"
)

// CheckRuntimeDependencies checks whether the local VM runtime dependencies
// are installed for arch.
func CheckRuntimeDependencies(arch string) error {
	return execwrap.Installed(qemu.QemuRequiredBinaries(arch))
}

// CheckServerRuntimeDependencies checks whether local server runtime
// dependencies are installed.
func CheckServerRuntimeDependencies() error {
	return execwrap.Installed([]string{"mkcert"})
}

// MkcertRootCAPath returns the path to mkcert's local root CA certificate.
func MkcertRootCAPath() (string, error) {
	cmd := execwrap.Command("mkcert", "-CAROOT")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("get mkcert CA root: %w", err)
	}
	rootCAPath := filepath.Join(strings.TrimSpace(string(output)), "rootCA.pem")
	if _, err := os.Stat(rootCAPath); err != nil {
		return "", fmt.Errorf("stat mkcert root CA %s: %w", rootCAPath, err)
	}
	return rootCAPath, nil
}

// MkcertTrustInstalled reports whether mkcert's local CA is trusted by the
// host system trust store.
func MkcertTrustInstalled() (bool, error) {
	rootCAPath, err := MkcertRootCAPath()
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	rootCA, err := os.ReadFile(rootCAPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read mkcert root CA: %w", err)
	}
	block, _ := pem.Decode(rootCA)
	if block == nil {
		return false, fmt.Errorf("parse mkcert root CA: PEM block not found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("parse mkcert root CA: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return false, fmt.Errorf("load system trust store: %w", err)
	}
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err == nil, nil
}
