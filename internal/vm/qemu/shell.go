// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/podplane/podplane/internal/execwrap"
	"github.com/podplane/podplane/internal/vm"
)

// Shell opens a shell session into a qemu VM, or if a command is provided,
// executes that command instead of opening interactive shell
func (m *Qemu) Shell(ctx context.Context, command string, sshPort int, identityFile string, opts vm.ShellOptions) ([]byte, error) {
	// Check if VM is running
	running, err := m.Running()
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, fmt.Errorf("no VM is currently running")
	}
	args := []string{
		"-p", strconv.Itoa(sshPort),
		// trust any server key, prevent prompts and warnings when connecting
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
	}
	// add identity (public key) file if set
	if identityFile != "" {
		args = append(args, "-i", identityFile)
	}
	// set user and host
	args = append(args, "debian@127.0.0.1")
	if command != "" {
		args = append(args, command)
	}

	if command != "" {
		if ctx == nil {
			ctx = context.Background()
		}
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}
		cmd := exec.CommandContext(ctx, "ssh", args...)
		if opts.Stdout != nil || opts.Stderr != nil {
			cmd.Stdout = opts.Stdout
			cmd.Stderr = opts.Stderr
			if err := cmd.Run(); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, err
			}
			return nil, nil
		}
		output, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return output, ctx.Err()
		}
		return output, err
	}

	if ctx != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	// Verify SSH is reachable before handing off to the ssh client.
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", sshPort))
	fmt.Printf("Checking SSH connectivity on %s...\n", addr)
	conn, dialErr := net.DialTimeout("tcp", addr, 5*time.Second)
	if dialErr != nil {
		return nil, fmt.Errorf("VM is running but SSH is not reachable on %s (is the VM still booting?): %w", addr, dialErr)
	}
	conn.Close()

	fmt.Println("Connecting via SSH...")

	// Open shell into the VM
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, "ssh", args...)
	} else {
		cmd = execwrap.Command("ssh", args...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// SSH exits 255 for connection-level failures (e.g. key exchange
		// reset). Wrap with a hint so the user knows the VM may still be
		// booting even though the port is open.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 255 {
			return nil, fmt.Errorf("SSH connection failed (the VM may still be booting — try again in a few seconds): %w", err)
		}
		return nil, err
	}
	return nil, nil
}
