// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"golang.org/x/term"
)

// Console attaches the terminal to the VM's serial console Unix socket.
func (m *Qemu) Console() error {
	running, err := m.Running()
	if err != nil {
		return err
	}
	if !running {
		return fmt.Errorf("no VM is currently running")
	}

	path := m.SerialConsolePath()
	conn, err := net.Dial("unix", path)
	if err != nil {
		return fmt.Errorf("connect to serial console %s: %w", path, err)
	}
	defer func() { _ = conn.Close() }()

	_, _ = fmt.Fprintln(os.Stderr, "Connected to VM serial console. Press Ctrl-] to detach.")
	if err := withRawTerminal(os.Stdin, func() error {
		return attachConsole(conn, os.Stdin, os.Stdout)
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stderr, "\nDetached from VM serial console.")
	return nil
}

func withRawTerminal(file *os.File, fn func() error) error {
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return fn()
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set terminal raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(fd, state)
	}()
	return fn()
}

func attachConsole(conn net.Conn, stdin io.Reader, stdout io.Writer) error {
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(stdout, conn)
		errCh <- err
	}()
	go func() {
		errCh <- copyConsoleInput(conn, stdin)
	}()

	err := <-errCh
	_ = conn.Close()
	if ignoreConsoleError(err) {
		return nil
	}
	return err
}

func copyConsoleInput(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 1)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if buf[0] == 0x1d { // Ctrl-]
				return nil
			}
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func ignoreConsoleError(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}
