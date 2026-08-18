// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/podplane/podplane/internal/clusterauth"
	"github.com/podplane/podplane/internal/config"
	"github.com/spf13/cobra"
)

// newHooksDockerCredentialsCmd creates the Docker credential helper hook command.
func newHooksDockerCredentialsCmd(c *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docker-credentials <get|store|erase|list>",
		Short: "Docker credential helper for Podplane registry tokens",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "get":
				var serverURL string
				if err := json.NewDecoder(os.Stdin).Decode(&serverURL); err != nil {
					return err
				}
				serverURL = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(serverURL), "https://"), "http://")
				serverURL = strings.TrimSuffix(serverURL, "/")
				for _, local := range []bool{false, true} {
					entries, err := c.AuthListByCluster("", local)
					if err != nil {
						return err
					}
					for _, meta := range entries {
						summary, err := c.ClusterSummary(meta.ClusterID, local)
						if err != nil || summary.Registry.Hostname != serverURL || !summary.Registry.Ingress.Enabled {
							continue
						}
						ref := config.AuthRef{Subject: meta.Sub, ClusterID: meta.ClusterID, Local: local}
						token, err := clusterauth.ResolveToken(c, ref)
						if err != nil {
							return err
						}
						return json.NewEncoder(os.Stdout).Encode(map[string]string{
							"ServerURL": serverURL,
							"Username":  "<token>",
							"Secret":    token,
						})
					}
				}
				return fmt.Errorf("no Podplane registry credentials found for %s", serverURL)
			case "store", "erase":
				_, _ = io.Copy(io.Discard, os.Stdin)
				return nil
			case "list":
				_, _ = io.WriteString(os.Stdout, "{}\n")
				return nil
			default:
				return fmt.Errorf("unsupported docker credential helper action %q", args[0])
			}
		},
	}
	return cmd
}
