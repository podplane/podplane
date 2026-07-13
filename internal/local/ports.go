// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"fmt"
	"net"

	"github.com/podplane/podplane/internal/clusterspec"
	"github.com/podplane/podplane/internal/vm"
)

type localVMPortForward struct {
	SetPort         func(*portState, int)
	HostBindAddress string
	DefaultHostPort int
	GuestPort       int
}

// localVMPortForwards lists the host-to-guest TCP forwards used by local VMs.
var localVMPortForwards = []localVMPortForward{
	{SetPort: func(ports *portState, port int) { ports.SSH = port }, HostBindAddress: "127.0.0.1", DefaultHostPort: 2222, GuestPort: 22},
	{SetPort: func(ports *portState, port int) { ports.KubernetesAPI = port }, HostBindAddress: "127.0.0.1", DefaultHostPort: clusterspec.KubernetesAPISecurePort, GuestPort: clusterspec.KubernetesAPISecurePort},
	{SetPort: func(ports *portState, port int) { ports.TraefikDashboard = port }, HostBindAddress: "127.0.0.1", DefaultHostPort: 8081, GuestPort: 8080},
	{SetPort: func(ports *portState, port int) { ports.Registry = port }, HostBindAddress: "127.0.0.1", DefaultHostPort: 5001, GuestPort: 5000},
	{SetPort: func(ports *portState, port int) { ports.TraefikHTTPS = port }, HostBindAddress: "127.0.0.1", DefaultHostPort: 8443, GuestPort: 443},
}

// allocateLocalVMPorts chooses available host ports for every local VM port
// forward, preferring each forward's documented default port when available.
func allocateLocalVMPorts() ([]vm.PortForward, portState, error) {
	forwards := make([]vm.PortForward, 0, len(localVMPortForwards))
	var ports portState
	reserved := map[int]bool{}
	for _, forward := range localVMPortForwards {
		port, err := findFreePort(forward.HostBindAddress, forward.DefaultHostPort, reserved)
		if err != nil {
			return nil, portState{}, fmt.Errorf("allocate local VM port: %w", err)
		}
		forwards = append(forwards, vm.PortForward{
			HostBindAddress: forward.HostBindAddress,
			HostPort:        port,
			GuestPort:       forward.GuestPort,
		})
		forward.SetPort(&ports, port)
		reserved[port] = true
	}
	return forwards, ports, nil
}

// findFreePort returns preferredPort when it is available on host; otherwise it
// asks the OS for an available ephemeral port. Ports marked reserved are skipped
// so one allocation pass does not return duplicates.
func findFreePort(host string, preferredPort int, reserved map[int]bool) (int, error) {
	if preferredPort > 0 && !reserved[preferredPort] {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", preferredPort)))
		if err == nil {
			_ = ln.Close()
			return preferredPort, nil
		}
	}

	for {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return 0, err
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if !reserved[port] {
			return port, nil
		}
	}
}
