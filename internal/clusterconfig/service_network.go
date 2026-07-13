// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterconfig

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net/netip"
)

var defaultServiceCIDRs = []string{"198.18.0.0/15", "fdc6::/108"}

const ipv6DNSOffset = 65535

// ServiceNetwork contains canonical service networks and reserved addresses.
type ServiceNetwork struct {
	CIDRs   []string
	API     []netip.Addr
	CoreDNS []netip.Addr
}

// ServiceNetworkFromCIDRs validates Kubernetes service CIDRs and returns their reserved addresses.
func ServiceNetworkFromCIDRs(cidrs []string) (ServiceNetwork, error) {
	if len(cidrs) == 0 {
		cidrs = defaultServiceCIDRs
	}
	if len(cidrs) > 2 {
		return ServiceNetwork{}, fmt.Errorf("service_cidr supports at most two networks")
	}
	var out ServiceNetwork
	seen4, seen6 := false, false
	for i, raw := range cidrs {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return out, fmt.Errorf("service_cidr[%d] %q is invalid: %w", i, raw, err)
		}
		if p != p.Masked() {
			return out, fmt.Errorf("service_cidr[%d] %q is not canonical (use %q)", i, raw, p.Masked())
		}
		is4 := p.Addr().Is4()
		if (is4 && seen4) || (!is4 && seen6) {
			return out, fmt.Errorf("service_cidr contains duplicate %s networks", map[bool]string{true: "IPv4", false: "IPv6"}[is4])
		}
		if i == 0 && !is4 {
			return out, fmt.Errorf("service_cidr must be IPv4-primary; IPv6-primary configurations are unsupported")
		}
		seen4, seen6 = seen4 || is4, seen6 || !is4
		dns := coreDNSAddr(p)
		if !p.Contains(dns) || dns.Compare(p.Addr().Next()) <= 0 {
			return out, fmt.Errorf("service_cidr %q has insufficient capacity for the Kubernetes API and DNS services", raw)
		}
		out.CIDRs = append(out.CIDRs, p.String())
		out.API = append(out.API, p.Addr().Next())
		out.CoreDNS = append(out.CoreDNS, dns)
	}
	return out, nil
}

// coreDNSAddr returns the address reserved for CoreDNS within p.
func coreDNSAddr(p netip.Prefix) netip.Addr {
	offset := uint64(ipv6DNSOffset)
	if p.Addr().Is4() {
		hostBits := 32 - p.Bits()
		if hostBits < 2 {
			return netip.Addr{}
		}
		offset = uint64(1)<<hostBits - 2
	}
	b := p.Addr().As16()
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	lo, carry := bits.Add64(lo, offset, 0)
	hi, _ = bits.Add64(hi, 0, carry)
	binary.BigEndian.PutUint64(b[:8], hi)
	binary.BigEndian.PutUint64(b[8:], lo)
	addr := netip.AddrFrom16(b)
	if p.Addr().Is4() {
		addr = addr.Unmap()
	}
	return addr
}
