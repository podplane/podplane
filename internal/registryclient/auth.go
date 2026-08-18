// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package registryclient

import (
	"fmt"

	"github.com/podplane/podplane/internal/clusterauth"
	"github.com/podplane/podplane/internal/config"
	"github.com/podplane/podplane/internal/kubectl"
)

// resolvePushToken returns a current Podplane OIDC ID token for the selected cluster.
func resolvePushToken(c *config.Config, clusterID string, local bool, kubeContext, kubeconfig string) (string, error) {
	user, err := kubectl.UserFromContext(kubeContext, kubeconfig)
	if err != nil {
		return "", err
	}
	authConfig := c
	if local {
		localAuthConfig, restoreKeyringPass, err := config.InitWithLocalKeyring()
		if err != nil {
			return "", err
		}
		defer restoreKeyringPass()
		authConfig = localAuthConfig
	}
	sub := kubectl.SubFromCredentialsKey(user, clusterID, local)
	if sub == "" {
		entries, err := authConfig.AuthListByCluster(clusterID, local)
		if err != nil {
			return "", err
		}
		if len(entries) == 1 {
			sub = entries[0].Sub
		}
	}
	if sub == "" {
		return "", fmt.Errorf("could not resolve cached Podplane auth for cluster %q; run `podplane login`", clusterID)
	}
	return clusterauth.ResolveToken(authConfig, config.AuthRef{Subject: sub, ClusterID: clusterID, Local: local})
}
