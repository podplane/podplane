// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/podplane/podplane/internal/execwrap"
)

// EnsureDataDisk creates a sparse data disk or grows it before the VM starts.
// An empty size never resizes an existing disk.
func (m *Qemu) EnsureDataDisk(size string) error {
	path := m.DataDiskPath()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat data disk: %w", err)
	}
	exists := err == nil
	if exists && size == "" {
		return nil
	}
	if size == "" {
		size = "256G"
	}
	// The raw format's required size is the virtual size. Let qemu-img parse
	// its own size syntax rather than maintaining a second, divergent parser.
	var requested struct {
		Bytes uint64 `json:"required"`
	}
	if err := qemuImageJSON(&requested, "measure", "--output=json", "-O", "raw", "--size", size); err != nil {
		return fmt.Errorf("invalid data disk size %q: %w", size, err)
	}
	if requested.Bytes == 0 {
		return fmt.Errorf("data disk size must be greater than zero")
	}
	// qcow2 rounds its virtual size up to a whole sector on creation.
	requested.Bytes = (requested.Bytes + 511) &^ 511
	pid, err := m.PID()
	if err != nil {
		return err
	}
	if exists {
		var current struct {
			Bytes uint64 `json:"virtual-size"`
		}
		if err := qemuImageJSON(&current, "info", "--force-share", "--output=json", path); err != nil {
			return err
		}
		if requested.Bytes == current.Bytes {
			return nil
		}
		if pid != 0 {
			return fmt.Errorf("cannot change data disk size while the VM is running; stop it first")
		}
		if requested.Bytes < current.Bytes {
			return fmt.Errorf("shrinking the data disk is unsupported; use `podplane local stop --rm` to recreate it")
		}
		return runQemuImage("resize", path, strconv.FormatUint(requested.Bytes, 10))
	}
	if pid != 0 {
		return fmt.Errorf("cannot add a data disk while the VM is running; stop it first")
	}
	if err := os.MkdirAll(m.VMImageDirectory(), 0o755); err != nil {
		return fmt.Errorf("create data disk directory: %w", err)
	}
	return runQemuImage("create", "-f", "qcow2", path, size)
}

// qemuImageJSON runs a qemu-img subcommand and decodes its JSON output into value.
func qemuImageJSON(value any, args ...string) error {
	cmd := execwrap.Command("qemu-img", args...)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("qemu-img %s: %w", args[0], err)
	}
	if err := json.Unmarshal(output, value); err != nil {
		return fmt.Errorf("decode qemu-img %s: %w", args[0], err)
	}
	return nil
}

// runQemuImage runs a qemu-img subcommand and includes its output in errors.
func runQemuImage(args ...string) error {
	output, err := execwrap.Command("qemu-img", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img %s: %w: %s", args[0], err, output)
	}
	return nil
}
