// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"fmt"
	"path/filepath"
)

const localOIDCHostname = "oidc.localhost"

const LocalClusterConfigFilename = "cluster.jsonc"

// ClusterDataDir returns the users data directory for a local cluster.
func ClusterDataDir(dataDir, clusterID string) string {
	return filepath.Join(dataDir, "local", clusterID)
}

// ClusterConfigPath returns the generated cluster config path for a local
// cluster.
func ClusterConfigPath(dataDir, clusterID string) string {
	return filepath.Join(ClusterDataDir(dataDir, clusterID), LocalClusterConfigFilename)
}

// UserdataDir returns the directory in which the rendered user-data file is
// written for a given cluster.
//
// @see: https://cloudinit.readthedocs.io/en/latest/reference/datasources/nocloud.html#source-files
func (l *Local) UserdataDir(clusterID string) string {
	return ClusterDataDir(l.dataDir, clusterID)
}

// UserdataPath returns the path to the user-data file for a given cluster.
// The file lives at <UserdataDir>/user-data.
func (l *Local) UserdataPath(clusterID string) string {
	return filepath.Join(l.UserdataDir(clusterID), "user-data")
}

// LocalServerURL returns the HTTP URL to the local server.
func (l *Local) LocalServerURL() (string, error) {
	if l.webserverPIDFile.PID() == 0 {
		return "", fmt.Errorf("local server PID not found")
	}
	host := l.webserverPIDFile.GetData("host")
	port := l.webserverPIDFile.GetData("http_port")
	if host == "" || port == "" {
		return "", fmt.Errorf("local server PID file missing HTTP address or port")
	}
	return fmt.Sprintf("http://%s:%s", host, port), nil
}

// LocalServerPort returns the HTTP port of the local server.
func (l *Local) LocalServerPort() (string, error) {
	if l.webserverPIDFile.PID() == 0 {
		return "", fmt.Errorf("local server PID not found")
	}
	port := l.webserverPIDFile.GetData("http_port")
	if port == "" {
		return "", fmt.Errorf("local server PID file missing HTTP port")
	}
	return port, nil
}

// LocalServerHTTPSPort returns the HTTPS port of the local server.
func (l *Local) LocalServerHTTPSPort() (string, error) {
	if l.webserverPIDFile.PID() == 0 {
		return "", fmt.Errorf("local server PID not found")
	}
	port := l.webserverPIDFile.GetData("https_port")
	if port == "" {
		return "", fmt.Errorf("local server PID file missing HTTPS port")
	}
	return port, nil
}

// LocalIngressURL returns the browser-facing local ingress proxy URL for this
// cluster.
func (l *Local) LocalIngressURL() (string, error) {
	port := l.webserverPIDFile.GetData("ingress_https_port")
	if port == "" {
		return "", fmt.Errorf("local server PID file missing ingress HTTPS port")
	}
	return fmt.Sprintf("https://%s.localhost:%s", l.clusterID, port), nil
}

// LocalKubernetesAPIURL returns the host-facing Kubernetes API URL routed
// through the reserved local ingress proxy hostname <cluster-id>.k8s.localhost
func (l *Local) LocalKubernetesAPIURL() (string, error) {
	port := l.webserverPIDFile.GetData("ingress_https_port")
	if port == "" {
		return "", fmt.Errorf("local server PID file missing ingress HTTPS port")
	}
	return fmt.Sprintf("https://%s:%s", LocalKubernetesAPIHostname(l.clusterID), port), nil
}

// hostForwardedKubernetesAPIURL returns a resolver that maps a local cluster ID
// to its direct QEMU host-forwarded kube-apiserver URL. This is intentionally
// not LocalKubernetesAPIURL, which returns the public local-ingress URL.
func hostForwardedKubernetesAPIURL(runtimeDir string) func(string) (string, error) {
	return func(clusterID string) (string, error) {
		state, err := readState(runtimeDir, clusterID)
		if err != nil {
			return "", fmt.Errorf("read local cluster state: %w", err)
		}
		if state.Ports.KubernetesAPI == 0 {
			return "", fmt.Errorf("local cluster state is missing kubernetes api port")
		}
		return fmt.Sprintf("https://127.0.0.1:%d", state.Ports.KubernetesAPI), nil
	}
}

// CloudInitServerURL returns the URL to the local server cloud-init base URL
// for a given cluster.
func (l *Local) CloudInitServerURL(hostMachineAddr, clusterID string) (string, error) {
	localServerPort, err := l.LocalServerPort()
	if err != nil {
		return "", err
	}
	if hostMachineAddr == "" || clusterID == "" {
		return "", fmt.Errorf("hostMachineAddr and clusterID must be set")
	}
	return fmt.Sprintf("http://%s:%s/cloud-init/%s/", hostMachineAddr, localServerPort, clusterID), nil
}

// DepsServerURL returns the URL to the local server deps cache base URL.
//
// If hostAddr is set and port is not, it will use the local server HTTP port.
// If hostAddr is not set, it will default to the local server URL.
func (l *Local) DepsServerURL(hostAddr, port string) (string, error) {
	// TODO: allow override via env var
	if port == "" {
		localServerPort, err := l.LocalServerPort()
		if err != nil {
			return "", err
		}
		port = localServerPort
	}
	baseURL := fmt.Sprintf("http://%s:%s", hostAddr, port)
	if hostAddr == "" {
		var err error
		baseURL, err = l.LocalServerURL()
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s/deps", baseURL), nil
}

// OIDCServerURL returns the URL to the local server fake HTTPS OIDC issuer.
func (l *Local) OIDCServerURL(hostAddr string) (string, error) {
	if hostAddr == "" {
		return "", fmt.Errorf("hostAddr must be set")
	}
	port, err := l.LocalServerHTTPSPort()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s:%s/oidc", localOIDCHostname, port), nil
}

// OIDCCACertPath returns the local server CA certificate path for the fake
// HTTPS OIDC issuer.
func (l *Local) OIDCCACertPath() string {
	return filepath.Join(l.dataDir, "local-server", "tls", "oidc", "ca.pem")
}

// OIDCKeyPath returns the local server's OIDC signing key path. The key is
// shared between the running local server and any in-process callers that
// need to mint tokens (e.g. the apiserver readiness probe).
func (l *Local) OIDCKeyPath() string {
	return filepath.Join(l.dataDir, "local-server", "oidc-key.pem")
}

// S3ServerURL returns the URL to the local server fake S3 endpoint for a
// given host-from-guest address.
func (l *Local) s3ServerURL(hostAddr, kind string) (string, error) {
	port, err := l.LocalServerPort()
	if err != nil {
		return "", err
	}
	if hostAddr == "" {
		return "", fmt.Errorf("hostAddr must be set")
	}
	return fmt.Sprintf("http://%s:%s/s3/%s", hostAddr, port, kind), nil
}

// S3DataServerURL returns the local fake S3 endpoint for durable data buckets.
func (l *Local) S3DataServerURL(hostAddr string) (string, error) {
	return l.s3ServerURL(hostAddr, "data")
}

// S3CacheServerURL returns the local fake S3 endpoint for cache-backed buckets.
func (l *Local) S3CacheServerURL(hostAddr string) (string, error) {
	return l.s3ServerURL(hostAddr, "cache")
}

// VaultServerURL returns the local Vault/OpenBao-compatible base URL for this
// cluster. The OpenBao CSI provider appends /v1/... paths below this URL.
func (l *Local) VaultServerURL(hostAddr string) (string, error) {
	port, err := l.LocalServerHTTPSPort()
	if err != nil {
		return "", err
	}
	if hostAddr == "" {
		return "", fmt.Errorf("hostAddr must be set")
	}
	if l.clusterID == "" {
		return "", fmt.Errorf("clusterID must be set")
	}
	return fmt.Sprintf("https://%s:%s/vault/%s", hostAddr, port, l.clusterID), nil
}

// localNetsyBucketName returns the fake S3 bucket name used for a local
// cluster's Netsy datastore.
func localNetsyBucketName(clusterID string) string {
	return fmt.Sprintf("%s-netsy", clusterID)
}

// localTelemetryBucketName returns the fake S3 bucket name used for a local
// cluster's telemetry data.
func localTelemetryBucketName(clusterID string) string {
	return fmt.Sprintf("%s-telemetry", clusterID)
}

// localS3BucketDir returns the on-disk data directory for a fake S3 bucket.
func localS3BucketDir(dataDir, bucket string) string {
	return filepath.Join(dataDir, "s3", "buckets", bucket)
}

// localS3MetadataDir returns the on-disk metadata directory for a fake S3
// bucket.
func localS3MetadataDir(dataDir, bucket string) string {
	return filepath.Join(dataDir, "s3", "metadata", bucket)
}
