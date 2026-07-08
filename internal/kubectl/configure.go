// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package kubectl

import (
	"bytes"
	"fmt"
	"io"
)

// ConfigureClusterAccess configures kubectl access for a Podplane cluster by
// setting the cluster, credentials, and context entries used by the kubectl
// exec auth hook. When local is true the kubeconfig keys use a "local-"
// prefix so they don't collide with remote clusters.
func ConfigureClusterAccess(stdout io.Writer, clusterID, serverURL, sub, caCertPath string, local bool) error {
	if clusterID == "" {
		return fmt.Errorf("kubectl configure: cluster id is required")
	}
	if serverURL == "" {
		return fmt.Errorf("kubectl configure: server url is required")
	}
	if sub == "" {
		return fmt.Errorf("kubectl configure: user sub is required")
	}

	_, _ = fmt.Fprintf(stdout, "Configuring kubectl...\n")

	// Buffer kubectl's own output (e.g. "User ... set.") so it stays
	// quiet on success but is available for debugging on failure.
	var buf bytes.Buffer

	if err := SetCluster(&buf, clusterID, serverURL, caCertPath, local); err != nil {
		return fmt.Errorf("configure kubectl cluster: %w\n%s", err, buf.String())
	}

	if err := SetCredentials(&buf, sub, clusterID, local); err != nil {
		return fmt.Errorf("configure kubectl credentials: %w\n%s", err, buf.String())
	}

	if err := SetContext(&buf, sub, clusterID, local); err != nil {
		return fmt.Errorf("configure kubectl context: %w\n%s", err, buf.String())
	}

	return nil
}
