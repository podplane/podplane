// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package qemu

import (
	"fmt"

	"github.com/podplane/podplane/internal/vm"
)

// Networking constants
// Alternatives:
// NET_CIDR="172.18.0.0/16"
// NET_START="172.18.0.1" # 172.18.0.1
// NET_END="192.18.0.2"   # 172.18.255.254
// NET_MASK="255.255.0.0" # /16
const NET_SUBNET = "192.168.1.0"
const NET_MASK = "255.255.255.0"
const NET_CIDR = "192.168.1.0/24"
const NET_START = "192.168.1.1"
const NET_END = "192.168.1.20"
const NET_MACPREFIX = "52:52:00:00:00:"

func NetMacAddress(suffix string) string {
	if len(suffix) == 1 {
		return NET_MACPREFIX + "0" + suffix
	} else {
		return NET_MACPREFIX + suffix
	}
}

// QemuRequiredBinaries returns the required binaries for qemu
func QemuRequiredBinaries(arch string) []string {
	return []string{
		QemuBinary(arch),
		"qemu-img",
	}
}

// QemuBinary returns the qemu binary name based on the arch
func QemuBinary(arch string) string {
	switch arch {
	case "amd64":
		return "qemu-system-x86_64"
	case "arm64":
		return "qemu-system-aarch64"
	default:
		panic("unsupported architecture")
	}
}

// QemuArguments builds the command arguments to pass to qemu based on the
// architecture specified. cloudInitDataISOPath is the path to a NoCloud
// cidata ISO that delivers user-data to the guest. When directBoot is present,
// QEMU loads the kernel/initrd directly and the qcow2 remains attached as the
// virtio root disk. When monitor is true, qemu is also started with
// "-monitor stdio" so the operator can interact with it from the terminal
// that launched `podplane local start --monitor`.
func QemuArguments(monitor bool, arch, vmImage, cloudInitDataISOPath, serialConsolePath, cpus, memory string, directBoot *vm.DirectBootOptions, ports []vm.PortForward) []string {
	var biosImagePath string
	var args []string
	switch arch {
	case "amd64":
		// TODO: detect correct path
		biosImagePath = "/usr/share/OVMF/OVMF_CODE_4M.fd"
		args = []string{
			// use virtualisation (not emulation) with KVM hardware
			// acceleration. `virt` is an ARM machine type; x86_64 uses Q35.
			"-machine", "q35,accel=kvm",
			// networking: TODO
		}
	case "arm64":
		// TODO: detect correct path
		biosImagePath = "/opt/homebrew/share/qemu/edk2-aarch64-code.fd"
		args = []string{
			// use virtualisation (not emulation) with Hypervisor Framework (HVF)
			// hardware acceleration and support phyiscal memory over 4GB
			"-machine", "virt,accel=hvf,highmem=on",
			// networking via vmnet-shared/virtio-net-pci (host machine is NET_START)
			// "-nic", fmt.Sprintf(
			// 	"vmnet-shared,start-address=%s,end-address=%s,subnet=%s,subnet-mask=%s,mac=%s",
			// 	NET_START,
			// 	NET_END,
			// 	NET_SUBNET,
			// 	NET_MASK,
			// 	NetMacAddress("1"),
			// ),
			// note: the above will enable multi-VM clusters (networking between
			// them) but we will need something like socket_vmnet to do this
			// without requiring the user to sudo for qemu each time.
		}
	default:
		panic("unsupported architecture")
	}
	// Set defaults for cpus/memory if unset
	if cpus == "" {
		cpus = "2"
	}
	if memory == "" {
		memory = "4G"
	}
	// Arch-agnostic options
	args = append(args,
		// networking via user mode
		"-nic", qemuUserNetwork(arch, ports),
		// suppress QEMU's default graphical window; guest console is exposed via
		// the serial socket used by `podplane local console`
		"-display", "none",
		// random number generator (RNG) device for entropy
		"-object", "rng-random,id=rng0,filename=/dev/urandom",
		"-device", "virtio-rng-pci,rng=rng0",
		// guest sees same CPU as host (req. for acceleration)
		"-cpu", "host",
		// use 2 vCPUs
		"-smp", cpus,
		// use 4GB RAM
		"-m", memory,
	)
	if directBoot != nil {
		args = append(args,
			"-kernel", directBoot.KernelPath,
			"-initrd", directBoot.InitrdPath,
			"-append", NormalizeKernelCmdline(arch, directBoot.Cmdline),
		)
	} else {
		args = append(args,
			// bios image for firmware/GRUB fallback boot
			"-bios", biosImagePath,
			// machine image (preserve existing fallback firmware boot path)
			"-hda", vmImage,
		)
	}
	if directBoot != nil {
		args = append(args,
			// machine image
			"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", vmImage),
		)
	}
	args = append(args,
		// cloud-init cidata ISO (NoCloud datasource with cidata volume label)
		"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio,media=cdrom", cloudInitDataISOPath),
		// serial console socket for `podplane local console`
		"-serial", fmt.Sprintf("unix:%s,server=on,wait=off", serialConsolePath),
	)
	// Optional interactive monitor (enabled by --monitor flag).
	if monitor {
		args = append(args,
			// show console in separate monitor window
			"-monitor", "stdio",
		)
	}
	return args
}

func qemuUserNetwork(arch string, ports []vm.PortForward) string {
	arg := "user"
	if arch == "amd64" {
		arg += ",model=virtio-net-pci"
	}
	for _, forward := range ports {
		arg += fmt.Sprintf(
			",hostfwd=tcp:%s:%d-:%d",
			forward.HostBindAddress,
			forward.HostPort,
			forward.GuestPort,
		)
	}
	return arg
}
