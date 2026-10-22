// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"fmt"
	"path/filepath"

	"github.com/podplane/podplane/internal/pid"
)

// vmFileName returns a filename with a consistent prefix for a VM
func (m *Qemu) vmFileName(suffix string, extension string) string {
	return fmt.Sprintf("%s.%s.%s.%s", m.VMName, m.arch, suffix, extension)
}

// VMImageDirectory returns the path to the directory for VM image files
func (m *Qemu) VMImageDirectory() string {
	return filepath.Join(m.dataDir, "vms")
}

// VMImagePath returns the path to the VM image file for the given arch
func (m *Qemu) VMImagePath() string {
	directory := m.VMImageDirectory()
	filename := m.vmFileName("image", "qcow2")
	return filepath.Join(directory, filename)
}

// DataDiskPath returns the durable disk used by the guest's podplane LVM VG.
func (m *Qemu) DataDiskPath() string {
	return filepath.Join(m.VMImageDirectory(), m.vmFileName("data", "qcow2"))
}

// VMPIDFilename returns the filename to use for a VM PID file
func (m *Qemu) VMPIDFilename() string {
	return m.vmFileName("pid", "json")
}

// VMPIDDirectory return the path to the directory for VM PID files
func (m *Qemu) VMPIDDirectory() string {
	return filepath.Join(m.runtimeDir, "vms")
}

// VMPIDFile returns a pid.PIDFile for the VM
func (m *Qemu) VMPIDFile() (pid.PIDFile, error) {
	return pid.Init(pid.PIDOptions{
		Directory: m.VMPIDDirectory(),
		Filename:  m.VMPIDFilename(),
	})
}

// SerialConsolePath returns the path to the QEMU serial console Unix socket.
func (m *Qemu) SerialConsolePath() string {
	filename := m.vmFileName("console", "sock")
	return filepath.Join(m.runtimeDir, "vms", filename)
}

// CloudInitDataDiskPath returns the path to the cloud-init data ISO image for a VM
func (m *Qemu) CloudInitDataDiskPath() string {
	filename := m.vmFileName("cloudinitdata", "iso")
	// The cidata ISO is stored alongside the VM image in the vms directory.
	return filepath.Join(m.dataDir, "vms", filename)
}
