// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloud_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/infrafiles"
	"github.com/podplane/podplane/internal/tfstate"
)

// TestAWSAccountUsesSelectedCredentials exercises real SDK request signing
// against local STS, then derives a cluster backend from the resolved account.
func TestAWSAccountUsesSelectedCredentials(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credentials, []byte("[selected]\naws_access_key_id = profile-key\naws_secret_access_key = profile-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "absent"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "environment-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_IGNORE_CONFIGURED_ENDPOINT_URLS", "false")
	for _, profile := range []string{"selected", ""} {
		t.Run("profile="+profile, func(t *testing.T) {
			key, account := "environment-key", "234567890123"
			if profile != "" {
				key, account = "profile-key", "987654321098"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "Credential="+key+"/") {
					t.Errorf("request did not use %s: %s", key, r.Header.Get("Authorization"))
				}
				_, _ = fmt.Fprintf(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>%s</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`, account)
			}))
			defer server.Close()
			t.Setenv("AWS_ENDPOINT_URL_STS", server.URL)
			got, err := infrafiles.ConfirmAWSAccount(context.Background(), "eu-west-1", profile, true)
			if err != nil || got != account {
				t.Fatalf("account = %q, %v; want %s", got, err, account)
			}
			cfg, err := clusterconfig.NewDraftConfig("aws")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Cluster.Providers[0].Account = got
			backend, err := tfstate.ForCluster(cfg)
			if err != nil || !strings.Contains(backend.Bucket, account) {
				t.Fatalf("backend = %+v, %v", backend, err)
			}
		})
	}
	if _, err := infrafiles.ConfirmAWSAccount(context.Background(), "eu-west-1", "missing", true); err == nil {
		t.Fatal("missing profile was accepted")
	}
}
