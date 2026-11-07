// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package cloud_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podplane/podplane/internal/cloud"
)

// TestRoute53ZoneLookup exercises the real SDK against local Route53 responses,
// including profile selection, pagination, exact matching, and ambiguous zones.
func TestRoute53ZoneLookup(t *testing.T) {
	dir := t.TempDir()
	credentials := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credentials, []byte("[dns]\naws_access_key_id = dns-key\naws_secret_access_key = dns-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "absent"))
	t.Setenv("AWS_ACCESS_KEY_ID", "wrong-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wrong-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_IGNORE_CONFIGURED_ENDPOINT_URLS", "false")
	zone := func(name string, private bool) string {
		return fmt.Sprintf("<HostedZone><Id>/hostedzone/Z123</Id><Name>%s</Name><Config><PrivateZone>%t</PrivateZone></Config></HostedZone>", name, private)
	}
	for _, tc := range []struct {
		name, zones, next, second, id, want string
		denied                              bool
	}{
		{name: "public exact", zones: zone("test.example.com.", false)},
		{name: "parent is not exact", zones: zone("example.com.", false), want: "no public Route53"},
		{name: "private only", zones: zone("test.example.com.", true), want: "no public Route53"},
		{name: "public on next page", zones: zone("test.example.com.", true), next: "test.example.com.", second: zone("test.example.com.", false)},
		{name: "duplicate on next page", zones: zone("test.example.com.", false), next: "test.example.com.", second: zone("test.example.com.", false), want: "multiple public"},
		{name: "unrelated next page", zones: zone("test.example.com.", false), next: "unrelated.example.com."},
		{name: "explicit id", id: "Z123"},
		{name: "denied", denied: true, want: "AccessDenied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=dns-key/") {
					t.Error("Route53 request did not use supplied profile")
				}
				if tc.denied {
					w.WriteHeader(http.StatusForbidden)
					_, _ = fmt.Fprint(w, "<ErrorResponse><Error><Code>AccessDenied</Code><Message>Not authorized</Message></Error></ErrorResponse>")
					return
				}
				if tc.id != "" {
					if r.URL.Path != "/2013-04-01/hostedzone/Z123" {
						t.Errorf("unexpected ID lookup path: %s", r.URL.Path)
					}
					_, _ = fmt.Fprint(w, "<GetHostedZoneResponse>"+zone("example.com.", false)+"</GetHostedZoneResponse>")
					return
				}
				if r.URL.Query().Get("dnsname") != "test.example.com." {
					t.Errorf("unexpected DNS lookup: %s", r.URL)
				}
				zones, truncated, next := tc.zones, tc.next != "", ""
				if r.URL.Query().Get("hostedzoneid") != "" {
					zones, truncated = tc.second, false
				}
				if truncated {
					next = "<NextDNSName>" + tc.next + "</NextDNSName><NextHostedZoneId>Znext</NextHostedZoneId>"
				}
				_, _ = fmt.Fprintf(w, "<ListHostedZonesByNameResponse><HostedZones>%s</HostedZones><IsTruncated>%t</IsTruncated>%s</ListHostedZonesByNameResponse>", zones, truncated, next)
			}))
			defer server.Close()
			t.Setenv("AWS_ENDPOINT_URL", server.URL)
			t.Setenv("AWS_ENDPOINT_URL_ROUTE_53", server.URL)
			err := cloud.CheckRoute53Zone(t.Context(), "eu-west-1", "dns", "TEST.example.com", tc.id)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("lookup error = %v, want %q", err, tc.want)
			}
		})
	}
}
