// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"slices"
	"testing"

	"github.com/podplane/podplane/internal/vm"
)

func TestQemuArgumentsDirectBoot(t *testing.T) {
	boot := &vm.DirectBootOptions{KernelPath: "/cache/vmlinuz", InitrdPath: "/cache/initrd", Cmdline: "root=UUID=abc ro"}
	args := QemuArguments(false, "arm64", "/vms/os.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", boot, nil)

	assertContainsPair(t, args, "-kernel", "/cache/vmlinuz")
	assertContainsPair(t, args, "-initrd", "/cache/initrd")
	assertContainsPair(t, args, "-append", "root=UUID=abc ro rootwait systemd.firstboot=off systemd.debug_shell systemd.default_debug_tty=ttyAMA0 console=tty0 console=ttyAMA0,115200n8")
	assertContainsPair(t, args, "-drive", "file=/vms/os.qcow2,format=qcow2,if=virtio")
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

func TestQemuArgumentsFirmwareFallback(t *testing.T) {
	args := QemuArguments(false, "arm64", "/vms/os.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", nil, nil)

	if !slices.Contains(args, "-bios") {
		t.Fatal("firmware fallback should include -bios")
	}
	assertContainsPair(t, args, "-hda", "/vms/os.qcow2")
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

func TestQemuArgumentsUsesSelectedLocalPorts(t *testing.T) {
	ports := []vm.PortForward{
		{HostBindAddress: "127.0.0.1", HostPort: 3222, GuestPort: 22},
		{HostBindAddress: "127.0.0.1", HostPort: 16443, GuestPort: 6443},
		{HostBindAddress: "127.0.0.1", HostPort: 18081, GuestPort: 8080},
		{HostBindAddress: "127.0.0.1", HostPort: 15001, GuestPort: 5000},
		{HostBindAddress: "127.0.0.1", HostPort: 18443, GuestPort: 443},
	}
	args := QemuArguments(false, "arm64", "/vms/os.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", nil, ports)

	assertContainsPair(t, args, "-nic", "user,hostfwd=tcp:127.0.0.1:3222-:22,hostfwd=tcp:127.0.0.1:16443-:6443,hostfwd=tcp:127.0.0.1:18081-:8080,hostfwd=tcp:127.0.0.1:15001-:5000,hostfwd=tcp:127.0.0.1:18443-:443")
}

func TestQemuArgumentsUsesQ35ForAMD64(t *testing.T) {
	args := QemuArguments(false, "amd64", "/vms/os.qcow2", "/vms/cidata.iso", "/run/vms/console.sock", "2", "4G", nil, nil)

	assertContainsPair(t, args, "-machine", "q35,accel=kvm")
	assertContainsPair(t, args, "-nic", "user,model=virtio-net-pci")
	assertNotContains(t, args, "virt,accel=kvm,highmem=on")
}

func assertContainsPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Fatalf("args do not contain %s %q: %v", key, value, args)
}

func assertNotContains(t *testing.T, args []string, value string) {
	t.Helper()
	if slices.Contains(args, value) {
		t.Fatalf("args should not contain %q: %v", value, args)
	}
}
