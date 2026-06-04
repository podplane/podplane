// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"fmt"
	"os"

	"github.com/podplane/podplane/internal/vm"
)

// Shell opens a shell into the running local cluster VM
// If command is provided, executes that command instead
// of opening interactive shell.
func (m *Local) Shell(command string) error {
	state, err := readState(m.runtimeDir, m.clusterID)
	if err != nil {
		return err
	}
	sshPort := state.Ports.SSH
	if sshPort == 0 {
		return fmt.Errorf("state is missing ssh port")
	}
	privateKeyPath, err := SSHPrivateKeyPath(m.dataDir)
	if err != nil {
		return fmt.Errorf("failed to prepare SSH key for local VM: %w", err)
	}
	opts := vm.ShellOptions{}
	if command != "" {
		opts.Stdout = os.Stdout
		opts.Stderr = os.Stderr
	}
	_, err = m.vm.Shell(context.Background(), command, sshPort, privateKeyPath, opts)
	return err
}
