// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/podplane/podplane/internal/cloudinit"
	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/vm"
)

// Start starts a qemu VM. The userData string is the rendered cloud-init
// user-data script; it is written into a NoCloud cidata ISO and attached to
// the VM as a cdrom drive.
func (m *Qemu) Start(userData, cpus, memory, sshAuthorizedKey string, monitor bool, boot *vm.DirectBootOptions, ports []vm.PortForward) error {
	// Check VM already exists
	exists, err := m.Exists()
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("VM does not exist")
	}

	// Get PID file
	pidFile, err := m.VMPIDFile()
	if err != nil {
		return fmt.Errorf("failed to get VM PID file: %w", err)
	}

	// Check VM if is already running
	running, err := pidFile.IsRunning()
	if err != nil {
		return err
	}
	if running {
		return vm.ErrAlreadyRunning
	}

	// Path to VM image
	vmImage := m.VMImagePath()

	// Generate the cloud-init data ISO
	cloudInitDataISO := m.CloudInitDataDiskPath()
	metaData := fmt.Sprintf("instance-id: %q\nlocal-hostname: %q\n", m.clusterID, m.clusterID)
	if sshAuthorizedKey != "" {
		metaData += fmt.Sprintf("public-keys:\n  - %s\n", sshAuthorizedKey)
	}
	networkConfig := ""
	if m.arch == "amd64" {
		networkConfig = `version: 2
ethernets:
  primary:
    match:
      driver: virtio_net
    dhcp4: true
    dhcp6: false
`
	}
	if err := cloudinit.WriteCloudInitDataISO(cloudInitDataISO, userData, metaData, networkConfig); err != nil {
		return fmt.Errorf("failed to generate cloud-init data ISO: %w", err)
	}

	serialConsolePath := m.SerialConsolePath()
	if err := os.MkdirAll(filepath.Dir(serialConsolePath), 0755); err != nil {
		return fmt.Errorf("failed to create serial console socket directory: %w", err)
	}
	_ = os.Remove(serialConsolePath)

	// Start the VM
	output := m.output
	_, _ = fmt.Fprintln(output, "Starting VM...")
	cmd := execwrap.Command(
		QemuBinary(m.arch),
		QemuArguments(monitor, m.arch, vmImage, cloudInitDataISO, serialConsolePath, cpus, memory, boot, ports)...,
	)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}
	if cmd.Process == nil || cmd.Process.Pid == 0 {
		return fmt.Errorf("failed to start VM: process is nil or pid is zero")
	}

	// Update the PID file
	if err := pidFile.SetPID(cmd.Process.Pid); err != nil {
		return fmt.Errorf("failed to set PID: %w", err)
	}
	err = pidFile.Write()
	if err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	return nil
}
