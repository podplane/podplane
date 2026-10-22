// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"fmt"
	"os"
)

// Delete deletes a qemu VM
func (m *Qemu) Delete() error {
	// Check that VM is stopped
	pid, err := m.PID()
	if err != nil {
		return err
	}
	if pid != 0 {
		return fmt.Errorf("VM is still running. Please stop it first")
	}

	// Path to VM image
	vmImage := m.VMImagePath()

	// Delete the VM image
	if err := os.Remove(vmImage); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete VM image: %w", err)
	}

	// Delete the VM data disk image
	if err := os.Remove(m.DataDiskPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete data disk: %w", err)
	}

	// Delete the cloud-init data ISO (best-effort; may not exist)
	_ = os.Remove(m.CloudInitDataDiskPath())

	// Delete stopped-VM runtime artifacts (best-effort; may not exist). Running
	// VMs returned above, so removing stale runtime files here is safe.
	if pidFile, err := m.VMPIDFile(); err == nil {
		_ = pidFile.Clean()
	}
	_ = os.Remove(m.SerialConsolePath())

	return nil
}
