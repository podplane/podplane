// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterspec

import (
	"slices"
	"testing"
)

// TestNodeIngressCertificate checks the optional node-local serving contract
// without changing the certificates requested by ordinary or local clusters.
func TestNodeIngressCertificate(t *testing.T) {
	for _, domains := range [][]string{nil, {"example.com", "second.example.net"}} {
		certificates := Certificates("cluster", "api.example.com", []string{"198.18.0.1"}, domains)
		var ingress *Certificate
		for i := range certificates {
			if certificates[i].Name == "ingress.server" {
				ingress = &certificates[i]
			}
		}
		if len(domains) == 0 {
			if ingress != nil || slices.Contains(CertificateFiles("knc", false), "ingress.server") {
				t.Fatal("ordinary cluster requests an ingress certificate")
			}
			continue
		}
		if ingress == nil || ingress.Kind != "server" || ingress.TTL != 8760 || len(ingress.Organization) != 0 || len(ingress.URI) != 0 {
			t.Fatalf("invalid ingress serving certificate: %+v", ingress)
		}
		for _, name := range []string{"localhost", "example.com", "*.example.com", "second.example.net", "*.second.example.net"} {
			if !slices.Contains(ingress.DNS, name) {
				t.Errorf("ingress certificate missing DNS SAN %q", name)
			}
		}
		if !slices.Equal(ingress.IP, []string{"{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"}) {
			t.Errorf("ingress IP SANs = %v", ingress.IP)
		}
		for _, kind := range []string{"knc", "knd", "nst", "nat"} {
			want := kind == "knc" || kind == "knd"
			if got := slices.Contains(CertificateFiles(kind, true), "ingress.server"); got != want {
				t.Errorf("kind %s requests ingress certificate = %v, want %v", kind, got, want)
			}
		}
	}
}
