// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"slices"
	"testing"

	"github.com/podplane/podplane/internal/vm"
)

// TestQemuArgumentsDirectBoot checks kernel boot, virtio disks, and serial socket arguments.
func TestQemuArgumentsDirectBoot(t *testing.T) {
	boot := &vm.DirectBootOptions{KernelPath: "/cache/vmlinuz", InitrdPath: "/cache/initrd", Cmdline: "root=UUID=abc ro"}
	args := QemuArguments(false, "darwin", "arm64", "/vms/os.qcow2", "/vms/data.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", boot, nil)

	assertContainsPair(t, args, "-kernel", "/cache/vmlinuz")
	assertContainsPair(t, args, "-initrd", "/cache/initrd")
	assertContainsPair(t, args, "-append", "root=UUID=abc ro rootwait systemd.firstboot=off systemd.debug_shell systemd.default_debug_tty=ttyAMA0 console=tty0 console=ttyAMA0,115200n8")
	assertContainsPair(t, args, "-drive", "file=/vms/os.qcow2,format=qcow2,if=virtio")
	assertContainsPair(t, args, "-drive", "file=/vms/data.qcow2,if=none,id=data0,format=qcow2,discard=unmap,detect-zeroes=unmap")
	assertContainsPair(t, args, "-device", "virtio-blk-pci,drive=data0,serial=podplane-data")
	assertContainsPair(t, args, "-drive", "file=/vms/cidata.iso,format=raw,if=virtio,media=cdrom")
	assertContainsPair(t, args, "-serial", "unix:/run/vms/console.sock,server=on,wait=off")
	assertNotContains(t, args, "-smbios")
	if slices.Contains(args, "-bios") {
		t.Fatal("direct boot args should not include -bios")
	}
	if slices.Contains(args, "-qmp") {
		t.Fatal("QMP args should not be present")
	}
}

// TestQemuArgumentsFirmwareFallback checks firmware boot omits direct-boot and QMP options.
func TestQemuArgumentsFirmwareFallback(t *testing.T) {
	args := QemuArguments(false, "darwin", "arm64", "/vms/os.qcow2", "/vms/data.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", nil, nil)

	if !slices.Contains(args, "-bios") {
		t.Fatal("firmware fallback should include -bios")
	}
	assertContainsPair(t, args, "-hda", "/vms/os.qcow2")
	assertContainsPair(t, args, "-drive", "file=/vms/data.qcow2,if=none,id=data0,format=qcow2,discard=unmap,detect-zeroes=unmap")
	assertContainsPair(t, args, "-device", "virtio-blk-pci,drive=data0,serial=podplane-data")
	assertContainsPair(t, args, "-drive", "file=/vms/cidata.iso,format=raw,if=virtio,media=cdrom")
	assertContainsPair(t, args, "-serial", "unix:/run/vms/console.sock,server=on,wait=off")
	assertNotContains(t, args, "-smbios")
	if slices.Contains(args, "-kernel") || slices.Contains(args, "-initrd") || slices.Contains(args, "-append") {
		t.Fatalf("firmware fallback should not include direct boot args: %v", args)
	}
	if slices.Contains(args, "-qmp") || slices.Contains(args, "usb-kbd") || slices.Contains(args, "qemu-xhci,id=usb") {
		t.Fatalf("QMP/USB keyboard workaround args should not be present: %v", args)
	}
}

// TestQemuArgumentsUsesSelectedLocalPorts checks all selected TCP forwards reach the NIC option.
func TestQemuArgumentsUsesSelectedLocalPorts(t *testing.T) {
	ports := []vm.PortForward{
		{HostBindAddress: "127.0.0.1", HostPort: 3222, GuestPort: 22},
		{HostBindAddress: "127.0.0.1", HostPort: 16443, GuestPort: 6443},
		{HostBindAddress: "127.0.0.1", HostPort: 18081, GuestPort: 8080},
		{HostBindAddress: "127.0.0.1", HostPort: 15001, GuestPort: 5000},
		{HostBindAddress: "127.0.0.1", HostPort: 18443, GuestPort: 443},
	}
	args := QemuArguments(false, "darwin", "arm64", "/vms/os.qcow2", "/vms/data.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", nil, ports)

	assertContainsPair(t, args, "-nic", "user,hostfwd=tcp:127.0.0.1:3222-:22,hostfwd=tcp:127.0.0.1:16443-:6443,hostfwd=tcp:127.0.0.1:18081-:8080,hostfwd=tcp:127.0.0.1:15001-:5000,hostfwd=tcp:127.0.0.1:18443-:443")
}

// TestQemuArgumentsUsesQ35ForAMD64 checks x86 uses Q35 and host-native acceleration.
func TestQemuArgumentsUsesQ35ForAMD64(t *testing.T) {
	for _, tc := range []struct{ os, arch, machine string }{
		{"linux", "amd64", "q35,accel=kvm"},
		{"darwin", "amd64", "q35,accel=hvf"},
		{"linux", "arm64", "virt,accel=kvm,highmem=on"},
		{"darwin", "arm64", "virt,accel=hvf,highmem=on"},
	} {
		t.Run(tc.os+"-"+tc.arch, func(t *testing.T) {
			args := QemuArguments(false, tc.os, tc.arch, "/vms/os.qcow2", "/vms/data.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", nil, nil)
			assertContainsPair(t, args, "-machine", tc.machine)
			if tc.arch == "amd64" {
				assertContainsPair(t, args, "-nic", "user,model=virtio-net-pci")
				assertContainsPair(t, args, "-bios", "edk2-x86_64-code.fd")
			} else {
				assertContainsPair(t, args, "-bios", "edk2-aarch64-code.fd")
			}
		})
	}
}

// assertContainsPair fails unless an option and its value occur consecutively.
func assertContainsPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Fatalf("args do not contain %s %q: %v", key, value, args)
}

// assertNotContains fails if an unwanted argument is present.
func assertNotContains(t *testing.T, args []string, value string) {
	t.Helper()
	if slices.Contains(args, value) {
		t.Fatalf("args should not contain %q: %v", value, args)
	}
}
