// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import "testing"

// TestServiceNetworkFromCIDRs verifies service CIDR validation and reserved addresses.
func TestServiceNetworkFromCIDRs(t *testing.T) {
	tests := []struct {
		name    string
		cidrs   []string
		api     []string
		dns     []string
		wantErr bool
	}{
		{name: "defaults", api: []string{"198.18.0.1", "fdc6::1"}, dns: []string{"198.19.255.254", "fdc6::ffff"}},
		{name: "IPv4 only", cidrs: []string{"10.96.0.0/12"}, api: []string{"10.96.0.1"}, dns: []string{"10.111.255.254"}},
		{name: "dual stack", cidrs: []string{"10.96.0.0/12", "fd00:1234::/108"}, api: []string{"10.96.0.1", "fd00:1234::1"}, dns: []string{"10.111.255.254", "fd00:1234::ffff"}},
		{name: "IPv6 primary", cidrs: []string{"fd00::/108"}, wantErr: true},
		{name: "duplicate family", cidrs: []string{"10.96.0.0/12", "10.112.0.0/12"}, wantErr: true},
		{name: "noncanonical", cidrs: []string{"10.96.0.1/12"}, wantErr: true},
		{name: "too small", cidrs: []string{"10.0.0.0/31"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network, err := ServiceNetworkFromCIDRs(tt.cidrs)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ServiceNetworkFromCIDRs succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ServiceNetworkFromCIDRs error = %v", err)
			}
			if len(network.API) != len(tt.api) {
				t.Fatalf("len(API) = %d, want %d", len(network.API), len(tt.api))
			}
			for i, want := range tt.api {
				if got := network.API[i].String(); got != want {
					t.Fatalf("API[%d] = %q, want %q", i, got, want)
				}
			}
			if len(network.CoreDNS) != len(tt.dns) {
				t.Fatalf("len(CoreDNS) = %d, want %d", len(network.CoreDNS), len(tt.dns))
			}
			for i, want := range tt.dns {
				if got := network.CoreDNS[i].String(); got != want {
					t.Fatalf("CoreDNS[%d] = %q, want %q", i, got, want)
				}
			}
		})
	}
}
