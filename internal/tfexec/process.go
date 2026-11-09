// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// shutdownGrace gives Terraform time to stop providers and save state before
// forcibly stopping its process group. Repeated interrupts do not skip cleanup.
const shutdownGrace = 30 * time.Second

// runProcess owns the engine and its provider processes until they stop. Signal
// handling is scoped to execution so other CLI commands retain their own signal
// behavior. A separate group prevents a terminal interrupt from reaching the
// engine twice (once from the terminal and once from cancellation).
// A nil notice writes shutdown warnings to stderr.
func runProcess(ctx context.Context, cmd *exec.Cmd, notice func(string)) error {
	if notice == nil {
		notice = func(message string) { fmt.Fprintln(os.Stderr, message) }
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound inherited output pipes if the engine exits before its providers.
	cmd.WaitDelay = time.Second
	cleanup, err := relayTerminalInput(cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := cmd.Start(); err != nil {
		return err
	}
	// Even on a normal engine exit, do not leave provider children behind.
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		// Signal only the engine first: it coordinates provider shutdown and
		// releases the state lock. Killing providers here can lose state.
		notice(fmt.Sprintf("Stopping OpenTofu/Terraform; waiting up to %s for cleanup...", shutdownGrace))
		_ = cmd.Process.Signal(os.Interrupt)
	}
	timer := time.NewTimer(shutdownGrace)
	defer timer.Stop()
	select {
	case <-done: //nolint:errcheck // Cancellation is returned instead of the engine's exit status.
	case <-timer.C:
		notice("OpenTofu/Terraform did not stop; killing its process group. Check remote state and locks before retrying.")
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done //nolint:errcheck // Reap the engine; cancellation supersedes its exit status.
	}
	return ctx.Err()
}

// relayTerminalInput keeps the CLI in the terminal's foreground process group
// while allowing the isolated engine to read approval answers. Reading a tty
// directly from the engine's background group would stop it with SIGTTIN.
func relayTerminalInput(cmd *exec.Cmd) (func(), error) {
	input, ok := cmd.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return func() {}, nil
	}
	// A separate open avoids changing stdin/stdout's shared file flags.
	// Nonblocking reads allow cleanup even while an input line is incomplete.
	fd, err := unix.Open("/dev/tty", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	cmd.Stdin = reader
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = writer.Close() }()
		// Darwin does not support poll on /dev/tty.
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		buffer := make([]byte, 4096)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := unix.Read(fd, buffer)
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				select {
				case <-stop:
					return
				case <-tick.C:
				}
				continue
			}
			if err != nil || n == 0 {
				return
			}
			if _, err := writer.Write(buffer[:n]); err != nil {
				return
			}
		}
	}()
	return func() {
		close(stop)
		_ = writer.Close()
		_ = reader.Close()
		<-done
		_ = unix.Close(fd)
	}, nil
}
