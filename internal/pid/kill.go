// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package pid

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Kill stops an existing PIDFile process
func (p *PIDFile) Kill() error {
	// attempt to kill the PID process
	process, err := process.NewProcess(int32(p.PID()))
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}
	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}
	// Wait for up to 10 seconds for the process to exit
	for i := range 10 {
		isRunning, _ := process.IsRunning()
		if !isRunning {
			// Process has exited
			break
		}
		if i > 9 {
			// Process is still running after 10 seconds
			return fmt.Errorf("process %d is still running after 10 seconds", p.PID())
		}
		time.Sleep(time.Second)
	}
	// remove the PID file
	if err := p.Clean(); err != nil {
		return fmt.Errorf("failed to clean PID file: %w", err)
	}
	slog.Debug("process stopped", "pid", p.PID())
	// Remove the PID file
	return p.Clean()
}
