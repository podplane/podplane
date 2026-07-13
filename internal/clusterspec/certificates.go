// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package clusterspec

// Certificate describes one Nstance-issued certificate required by a Podplane VM.
type Certificate struct {
	Name         string
	Kind         string
	CN           string
	Organization []string
	DNS          []string
	IP           []string
	URI          []string
	TTL          int
}

// Certificates returns the certificate templates required by Podplane VMs.
func Certificates(netsyClusterIDTemplate, apiHostname string, apiServiceIPs []string) []Certificate {
	apiServerIPs := append(defaultIP(), apiServiceIPs...)
	apiServerDNS := append(defaultDNS(), "kube-apiserver.podplane.internal")
	if apiHostname != "" {
		apiServerDNS = append(apiServerDNS, apiHostname)
	}
	return []Certificate{
		clientCertificate("containerd.client"),
		clientCertificateWithCN("front-proxy.client", "front-proxy-client"),
		{
			Name:         "kube-apiserver.client",
			Kind:         "client",
			CN:           "kube-apiserver.client",
			Organization: []string{"system:masters"},
			DNS:          defaultDNS(),
			IP:           defaultIP(),
			URI:          []string{"netsy://" + netsyClusterIDTemplate + "/client/kube-apiserver"},
			TTL:          8760,
		},
		{
			Name: "kube-apiserver.server",
			Kind: "server",
			CN:   "kube-apiserver.server",
			DNS:  apiServerDNS,
			IP:   apiServerIPs,
			TTL:  8760,
		},
		clientCertificateWithCN("kube-controller-manager.client", "system:kube-controller-manager"),
		clientCertificateWithCN("kube-scheduler.client", "system:kube-scheduler"),
		clientCertificate("kube2iam.client"),
		{
			Name:         "kubelet.client",
			Kind:         "client",
			CN:           "system:node:{{ .Instance.ID }}",
			Organization: []string{"system:nodes"},
			DNS:          defaultDNS(),
			IP:           defaultIP(),
			TTL:          8760,
		},
		serverCertificate("kubelet.server"),
		netsyCertificate("netsy.client", "client", netsyClusterIDTemplate),
		netsyCertificate("netsy.server", "server", netsyClusterIDTemplate),
		serverCertificate("registry.server"),
	}
}

// CertificateFiles returns the certificate template names required by kind.
func CertificateFiles(kind string) []string {
	names := []string{
		"containerd.client",
		"kube2iam.client",
		"kubelet.client",
		"kubelet.server",
		"registry.server",
	}
	if kind == "knc" {
		names = append(names,
			"front-proxy.client",
			"kube-apiserver.client",
			"kube-apiserver.server",
			"kube-controller-manager.client",
			"kube-scheduler.client",
			"netsy.client",
			"netsy.server",
		)
	}
	return names
}

// clientCertificate returns a client certificate with the default name as CN.
func clientCertificate(name string) Certificate {
	return clientCertificateWithCN(name, name)
}

// clientCertificateWithCN returns a client certificate with an explicit CN.
func clientCertificateWithCN(name string, cn string) Certificate {
	return Certificate{Name: name, Kind: "client", CN: cn, DNS: defaultDNS(), IP: defaultIP(), TTL: 8760}
}

// serverCertificate returns a server certificate with default SANs.
func serverCertificate(name string) Certificate {
	return Certificate{Name: name, Kind: "server", CN: name, DNS: defaultDNS(), IP: defaultIP(), TTL: 8760}
}

// netsyCertificate returns a Netsy peer/client certificate.
func netsyCertificate(name string, kind string, clusterIDTemplate string) Certificate {
	cert := clientCertificateWithCN(name, name)
	cert.Kind = kind
	cert.URI = []string{"netsy://" + clusterIDTemplate + "/peer/{{ .Instance.ID }}"}
	return cert
}

// defaultDNS returns DNS SAN templates shared by most VM certificates.
func defaultDNS() []string {
	return []string{"{{ .Instance.Hostname }}", "localhost"}
}

// defaultIP returns IP SAN templates shared by most VM certificates.
func defaultIP() []string {
	return []string{"{{ .Instance.IP4 }}", "{{ .Instance.IP6 }}", "127.0.0.1", "::1"}
}
