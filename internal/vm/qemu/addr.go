// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

const qemuNodeIP = "10.0.2.15"

// LocalServerCertificateSANs returns QEMU guest addresses that may be used by
// workloads inside the VM to reach the Podplane local HTTPS server.
func LocalServerCertificateSANs() []string {
	return []string{qemuNodeIP}
}

// Addr returns the host machine address reachable from the QEMU guest.
func (m *Qemu) Addr() string {
	return "10.0.2.2"
}

// NodeIP returns the QEMU guest node IP reachable from local-cluster pods.
func (m *Qemu) NodeIP() string {
	return qemuNodeIP
}
