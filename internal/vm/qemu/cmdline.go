// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import "strings"

// NormalizeKernelCmdline adapts the manifest's raw boot-entry cmdline for
// local QEMU direct boot while preserving device/root arguments.
func NormalizeKernelCmdline(arch, raw string) string {
	fields := strings.Fields(raw)
	seen := map[string]bool{}
	out := make([]string, 0, len(fields)+3)
	hasRootwait := false
	hasConsole := false
	hasSystemdFirstboot := false
	hasSystemdDebugShell := false
	hasSystemdDefaultDebugTTY := false

	for _, field := range fields {
		if field == "$vt_handoff" || strings.HasPrefix(field, "BOOT_IMAGE=") {
			continue
		}
		if field == "rootwait" {
			hasRootwait = true
		}
		if strings.HasPrefix(field, "console=") {
			hasConsole = true
		}
		if strings.HasPrefix(field, "systemd.firstboot=") {
			hasSystemdFirstboot = true
		}
		if field == "systemd.debug_shell" || strings.HasPrefix(field, "systemd.debug_shell=") {
			hasSystemdDebugShell = true
		}
		if strings.HasPrefix(field, "systemd.default_debug_tty=") {
			hasSystemdDefaultDebugTTY = true
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}

	if !hasRootwait {
		out = append(out, "rootwait")
	}
	if !hasSystemdFirstboot {
		out = append(out, "systemd.firstboot=off")
	}
	if !hasSystemdDebugShell {
		out = append(out, "systemd.debug_shell")
	}
	if !hasSystemdDefaultDebugTTY {
		out = append(out, "systemd.default_debug_tty="+serialConsoleTTY(arch))
	}
	if !hasConsole {
		out = append(out, "console=tty0")
		out = append(out, "console="+serialConsoleTTY(arch)+",115200n8")
	}

	return strings.Join(out, " ")
}

// serialConsoleTTY selects the guest serial device for the architecture.
func serialConsoleTTY(arch string) string {
	switch arch {
	case "arm64":
		return "ttyAMA0"
	default:
		return "ttyS0"
	}
}
