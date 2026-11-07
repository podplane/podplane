// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package tfstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/clusterconfig"
	"github.com/podplane/podplane/internal/oidcconfig"
)

// TestBackendWriteConfig verifies each stack's bucket name, configuration
// file name, and generated backend settings.
func TestBackendWriteConfig(t *testing.T) {
	cluster := func(provider clusterconfig.Provider) func() (Backend, error) {
		return func() (Backend, error) {
			return ForCluster(&clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "demo", Providers: []clusterconfig.Provider{provider}}})
		}
	}
	tests := []struct {
		name       string
		backend    func() (Backend, error)
		wantBucket string
		wantFile   string
		wantConfig []string
	}{
		{
			name:       "AWS cluster",
			backend:    cluster(clusterconfig.Provider{Kind: "aws", Account: "123456789012", Region: "us-east-1", Profile: "prod"}),
			wantBucket: "demo-123456789012-tfstate",
			wantFile:   "podplane.cluster.tfbackend",
			wantConfig: []string{`bucket = "demo-123456789012-tfstate"`, `key = "podplane/cluster.tfstate"`, `region = "us-east-1"`, "encrypt = true", "use_lockfile = true", `profile = "prod"`},
		},
		{
			name:       "Google cluster",
			backend:    cluster(clusterconfig.Provider{Kind: "google", Project: "demo-project", Region: "australia-southeast1"}),
			wantBucket: "demo-demo-project-tfstate",
			wantFile:   "podplane.cluster.tfbackend",
			wantConfig: []string{`bucket = "demo-demo-project-tfstate"`, `prefix = "podplane/cluster"`},
		},
		{
			name: "AWS OIDC",
			backend: func() (Backend, error) {
				return ForOIDC(&oidcconfig.Config{OIDC: oidcconfig.OIDC{Hostname: "https://Auth.Example.com", Provider: oidcconfig.Provider{Kind: "aws", Account: "123456789012", Region: "us-west-2"}}})
			},
			wantBucket: "auth-example-com-123456789012-tfstate-c204c854ffe5f35a",
			wantFile:   "podplane.oidc.tfbackend",
			wantConfig: []string{`bucket = "auth-example-com-123456789012-tfstate-c204c854ffe5f35a"`, `key = "podplane/oidc.tfstate"`, `region = "us-west-2"`, "use_lockfile = true"},
		},
		{
			name: "Google OIDC",
			backend: func() (Backend, error) {
				return ForOIDC(&oidcconfig.Config{OIDC: oidcconfig.OIDC{Hostname: "auth.example.com", Provider: oidcconfig.Provider{Kind: "google", Project: "demo-project", Region: "us-central1", Zone: "us-central1-a"}}})
			},
			wantBucket: "auth-example-com-demo-project-tfstate-c204c854ffe5f35a",
			wantFile:   "podplane.oidc.tfbackend",
			wantConfig: []string{`bucket = "auth-example-com-demo-project-tfstate-c204c854ffe5f35a"`, `prefix = "podplane/oidc"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := tt.backend()
			if err != nil {
				t.Fatal(err)
			}
			if backend.Bucket != tt.wantBucket {
				t.Fatalf("Bucket = %q, want %q", backend.Bucket, tt.wantBucket)
			}
			dir := t.TempDir()
			if err := backend.WriteConfig(dir); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, tt.wantFile))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.wantConfig {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("config missing %q:\n%s", want, raw)
				}
			}
		})
	}
}

// TestBackendRejectsUnusableConfig verifies that unsupported clouds and
// bucket names S3 cannot accept are rejected before any cloud call.
func TestBackendRejectsUnusableConfig(t *testing.T) {
	if _, err := ForCluster(&clusterconfig.ClusterConfig{Cluster: clusterconfig.Cluster{ID: "demo", Providers: []clusterconfig.Provider{{Kind: "proxmox"}}}}); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("ForCluster() error = %v, want unsupported provider", err)
	}
	long := &oidcconfig.Config{OIDC: oidcconfig.OIDC{Hostname: "identity.platform-engineering.corp.example.com", Provider: oidcconfig.Provider{Kind: "aws", Account: "123456789012", Region: "us-east-1"}}}
	if b, err := ForOIDC(long); err != nil || len(b.Bucket) != 63 {
		t.Fatalf("ForOIDC() = %v, %v, want truncated 63-character bucket", b, err)
	}
}

// TestOIDCHostnameIdentity verifies normalization without dot/hyphen aliasing.
func TestOIDCHostnameIdentity(t *testing.T) {
	var buckets []string
	for _, host := range []string{"auth.example.com", "https://Auth.Example.COM.", "auth-example.com"} {
		b, err := ForOIDC(&oidcconfig.Config{OIDC: oidcconfig.OIDC{Hostname: host, Provider: oidcconfig.Provider{Kind: "aws", Account: "123456789012", Region: "us-east-1"}}})
		if err != nil {
			t.Fatal(err)
		}
		buckets = append(buckets, b.Bucket)
	}
	if buckets[0] != buckets[1] || buckets[0] == buckets[2] {
		t.Fatalf("hostname buckets = %v", buckets)
	}
}

// TestOIDCBucketOwnership exercises real SDK request/response handling without
// cloud access. Neither auto-approval nor pending recovery may adopt a bucket.
func TestOIDCBucketOwnership(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("STORAGE_EMULATOR_HOST", "http://storage.invalid")
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credentials, []byte(`{"type":"authorized_user","client_id":"test","client_secret":"test","refresh_token":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentials)
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	for _, cloud := range []string{"aws", "google"} {
		for _, mode := range []string{"new", "matching", "missing", "mismatch", "pending-mismatch"} {
			t.Run(cloud+"/"+mode, func(t *testing.T) {
				b, err := ForOIDC(&oidcconfig.Config{OIDC: oidcconfig.OIDC{Hostname: "auth.example.com", Provider: oidcconfig.Provider{Kind: cloud, Account: "123456789012", Project: "demo-project", Region: "us-east-1"}}})
				if err != nil {
					t.Fatal(err)
				}
				host := "auth.example.com"
				label := "yicmqvh74xzvuo4fecf5tcogbuofnvlm7wng4l2y6kfznul6a4zq"
				if strings.Contains(mode, "mismatch") {
					host, label = "other.example.com", "other"
				}
				status := "configured"
				if mode == "pending-mismatch" {
					status = "pending"
				}
				exists := mode != "new"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := ""
					switch {
					case strings.Contains(r.Host, "oauth"):
						body = `{"access_token":"test","token_type":"Bearer","expires_in":3600}`
					case strings.Contains(r.Host, "cloudresourcemanager"):
						body = `{"projectId":"demo-project","projectNumber":"123"}`
					case mode == "new" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
						raw, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						if !exists || r.URL.Query().Has("tagging") {
							markers := []string{"podplane:stack", "oidc", "podplane:hostname", "auth.example.com"}
							if cloud == "google" {
								markers = []string{"podplane-stack", "oidc", "podplane-hostname", label}
							}
							for _, marker := range markers {
								if !strings.Contains(string(raw), marker) {
									t.Errorf("bucket write omitted %q: %s", marker, raw)
								}
							}
						}
						exists = true
						if cloud == "google" {
							body = `{}`
						}
					case r.Method != http.MethodGet && r.Method != http.MethodHead:
						t.Errorf("unexpected bucket mutation: %s %s", r.Method, r.URL)
						http.Error(w, "unexpected mutation", http.StatusBadRequest)
						return
					case !exists:
						w.WriteHeader(http.StatusNotFound)
						return
					case cloud == "google":
						labels := map[string]string{"podplane-stack": "oidc", "podplane-hostname": label}
						if mode == "missing" {
							labels = nil
						}
						encoded, _ := json.Marshal(labels)
						body = fmt.Sprintf(`{"name":%q,"projectNumber":"123","labels":%s,"versioning":{"enabled":true},"iamConfiguration":{"uniformBucketLevelAccess":{"enabled":true},"publicAccessPrevention":"enforced"}}`, b.Bucket, encoded)
					case r.Method == http.MethodHead:
					case r.URL.Query().Has("tagging"):
						body = fmt.Sprintf(`<Tagging><TagSet><Tag><Key>podplane:stack</Key><Value>oidc</Value></Tag><Tag><Key>podplane:hostname</Key><Value>%s</Value></Tag><Tag><Key>podplane:tfstate</Key><Value>%s</Value></Tag></TagSet></Tagging>`, host, status)
						if mode == "missing" {
							body = `<Tagging><TagSet/></Tagging>`
						}
					case r.URL.Query().Has("versioning"):
						body = `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`
					case r.URL.Query().Has("publicAccessBlock"):
						body = `<PublicAccessBlockConfiguration><BlockPublicAcls>true</BlockPublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><IgnorePublicAcls>true</IgnorePublicAcls><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`
					case r.URL.Query().Has("encryption"):
						body = `<ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`
					default:
						t.Errorf("unexpected request: %s", r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				t.Setenv("AWS_ENDPOINT_URL_S3", server.URL)
				t.Setenv("STORAGE_EMULATOR_HOST", server.URL)
				// Google clones the default transport. Route both HTTP and TLS
				// dials to this plain local server, including OAuth and project lookup.
				dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
				}
				localTransport := &http.Transport{DialContext: dial, DialTLSContext: dial}
				defer localTransport.CloseIdleConnections()
				http.DefaultTransport = localTransport
				for _, check := range []func() error{
					func() error { return b.Preflight(t.Context()) },
					func() error {
						return b.Ensure(context.Background(), io.Discard, func(string) (bool, error) {
							if mode != "new" {
								t.Error("unexpected adoption/create confirmation")
							}
							return true, nil
						})
					},
					func() error { return b.Check(t.Context(), io.Discard) },
				} {
					err := check()
					if mode == "matching" || mode == "new" {
						if err != nil {
							t.Fatal(err)
						}
					} else if err == nil || !strings.Contains(err.Error(), "ownership") {
						t.Fatalf("error = %v, want ownership rejection", err)
					}
				}
			})
		}
	}
}
