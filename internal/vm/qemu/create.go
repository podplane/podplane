// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fatih/color"

	"github.com/podplane/podplane/internal/execwrap"
)

// Create creates a new Qemu VM image, using the supplied base image path
// as the qcow2 backing file.
func (m *Qemu) Create(baseImage string) error {
	// Check if VM already exists
	exists, err := m.Exists()
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("VM already exists")
	}

	if baseImage == "" {
		return fmt.Errorf("base image path is empty")
	}

	// Path to VM image
	vmImage := m.VMImagePath()

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(vmImage), 0755); err != nil {
		return fmt.Errorf("failed to create VM image parent directory: %w", err)
	}

	// Create the VM image
	output := m.output
	_, _ = fmt.Fprintln(output, "Creating VM image...")
	cmd := execwrap.Command("qemu-img", "create", "-F", "qcow2", "-b", baseImage, "-f", "qcow2", vmImage, "128G")
	var errBuf bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img create failed: %w\n%s", err, errBuf.String())
	}
	_, _ = color.New(color.FgGreen).Fprintln(output, "✓ VM image created successfully")
	return nil
}
