// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"net"
	"testing"
)

// TestFindFreePortUsesPreferredPortWhenAvailable checks an available preferred port is retained.
func TestFindFreePortUsesPreferredPortWhenAvailable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	preferred := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	got, err := findFreePort("127.0.0.1", preferred, nil)
	if err != nil {
		t.Fatalf("findFreePort error = %v", err)
	}
	if got != preferred {
		t.Fatalf("findFreePort = %d, want preferred port %d", got, preferred)
	}
}

// TestFindFreePortFallsBackWhenPreferredPortIsBusy checks allocation avoids a listening port.
func TestFindFreePortFallsBackWhenPreferredPortIsBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	busy := ln.Addr().(*net.TCPAddr).Port

	got, err := findFreePort("127.0.0.1", busy, nil)
	if err != nil {
		t.Fatalf("findFreePort error = %v", err)
	}
	if got == busy {
		t.Fatalf("findFreePort returned busy port %d", busy)
	}
}

// TestFindFreePortSkipsReservedPreferredPort checks allocation avoids ports reserved by earlier forwards.
func TestFindFreePortSkipsReservedPreferredPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	preferred := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	got, err := findFreePort("127.0.0.1", preferred, map[int]bool{preferred: true})
	if err != nil {
		t.Fatalf("findFreePort error = %v", err)
	}
	if got == preferred {
		t.Fatalf("findFreePort returned reserved port %d", preferred)
	}
}
