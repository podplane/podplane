// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/config"
)

// TestDockerCredentialsGet accepts Docker's plain-text input and returns its identity-token format.
func TestDockerCredentialsGet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(config.KeyringPassEnv, "test-password")
	c, err := config.Init()
	if err != nil {
		t.Fatal(err)
	}
	claims := fmt.Sprintf(`{"sub":"test-user","exp":%d}`, time.Now().Add(time.Hour).Unix())
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".test-signature"
	if err := c.AuthSet(config.AuthMetadata{Sub: "test-user", ClusterID: "test-cluster"}, config.AuthSecrets{IDToken: token}, false); err != nil {
		t.Fatal(err)
	}
	summary := config.ClusterSummary{ID: "test-cluster", Registry: clusterconfig.Registry{
		Hostname: "registry.example.com", Ingress: clusterconfig.RegistryIngress{Enabled: true},
	}}
	if err := c.SetClusterSummary(summary, false); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"registry.example.com", "https://registry.example.com/\n"} {
		var output bytes.Buffer
		cmd := newHooksDockerCredentialsCmd(c)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"get"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var credentials struct{ ServerURL, Username, Secret string }
		if err := json.Unmarshal(output.Bytes(), &credentials); err != nil {
			t.Fatal(err)
		}
		if credentials.ServerURL != summary.Registry.Hostname || credentials.Username != "<token>" || credentials.Secret != token {
			t.Fatal("helper did not return the registry's cached identity token in Docker format")
		}
	}
}
