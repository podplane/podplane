// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import "testing"

// TestNormalizeKernelCmdline checks bootloader tokens are removed and local boot defaults added.
func TestNormalizeKernelCmdline(t *testing.T) {
	raw := "BOOT_IMAGE=/boot/vmlinuz root=UUID=abc ro quiet $vt_handoff"
	got := NormalizeKernelCmdline("arm64", raw)
	want := "root=UUID=abc ro quiet rootwait systemd.firstboot=off systemd.debug_shell systemd.default_debug_tty=ttyAMA0 console=tty0 console=ttyAMA0,115200n8"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeKernelCmdlinePreservesExistingConsoleAndRootwait checks explicit boot settings remain unchanged.
func TestNormalizeKernelCmdlinePreservesExistingConsoleAndRootwait(t *testing.T) {
	raw := "root=/dev/vda1 ro rootwait systemd.firstboot=off systemd.debug_shell systemd.default_debug_tty=ttyS0 console=ttyS0,115200n8"
	got := NormalizeKernelCmdline("amd64", raw)
	if got != raw {
		t.Fatalf("got %q, want %q", got, raw)
	}
}
