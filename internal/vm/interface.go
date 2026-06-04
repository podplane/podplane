// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"context"
	"io"
	"time"
)

// DirectBootOptions contains host-side artifacts for direct kernel boot. A nil
// value means the backend should use its normal firmware boot path.
type DirectBootOptions struct {
	KernelPath string
	InitrdPath string
	Cmdline    string
}

// PortForward describes one host-to-guest TCP port forward needed by a local
// VM.
type PortForward struct {
	HostBindAddress string
	HostPort        int
	GuestPort       int
}

// ShellOptions configures non-interactive command execution in a VM.
type ShellOptions struct {
	Timeout time.Duration
	Stdout  io.Writer
	Stderr  io.Writer
}

// Manager is the backend interface used by local cluster lifecycle commands to
// create, start, access, stop, and delete a local VM.
type Manager interface {
	// HostAddr returns the address to the host machine from the guest machine
	Addr() string

	// SetOutput sets where user-facing lifecycle messages and child process
	// output are written.
	SetOutput(output io.Writer)

	// Create creates a new VM instance using the given base image path.
	// The base image is sourced from the deps cache (managed by the
	// internal/deps package).
	Create(baseImage string) error

	// Exists checks if a VM exists
	Exists() (bool, error)

	// Start starts a VM instance. The userData string is the rendered
	// cloud-init user-data script; the VM backend is responsible for
	// delivering it to the guest (e.g. via a cidata ISO). When monitor is
	// true, the underlying driver should attach an interactive monitor to
	// the VM (driver-defined, e.g. for qemu this enables -monitor stdio).
	Start(userData, cpus, memory, sshAuthorizedKey string, monitor bool, boot *DirectBootOptions, ports []PortForward) error

	// Running checks if a VM is currently running
	Running() (bool, error)

	// PID returns the process ID of the running VM, or 0 if not running
	PID() (int, error)

	// Shell opens a shell session into the VM, or if a command is provided,
	// executes that command instead of opening an interactive shell.
	Shell(ctx context.Context, command string, sshPort int, identityFile string, opts ShellOptions) ([]byte, error)

	// Console attaches to the VM's serial console for boot/login debugging.
	Console() error

	// Sync runs rsync to the VM.
	Sync(from, to, chown string, excludes []string, sshPort int, identityFile string) error

	// Stop stops a running VM instance
	Stop() error

	// Delete removes a VM instance
	Delete() error
}
