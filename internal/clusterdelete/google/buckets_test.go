// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// TestEmptyBucketGenerations checks deletion of current and archived object generations.
func TestEmptyBucketGenerations(t *testing.T) {
	deleted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			if r.URL.Query().Get("versions") != "true" {
				t.Error("archived generations excluded")
			}
			if r.URL.Query().Get("pageToken") == "next" {
				_, _ = fmt.Fprint(w, `{"items":[{"name":"data","generation":"2"}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"items":[{"name":"data","generation":"1"}],"nextPageToken":"next"}`)
			}
		case "DELETE":
			deleted[r.URL.Query().Get("generation")] = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s", r.Method)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client, err := storage.NewClient(t.Context(), option.WithEndpoint(server.URL+"/storage/v1/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	p := &Provider{storage: client}
	if err := p.EmptyBucket(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || !deleted["1"] || !deleted["2"] {
		t.Fatalf("deleted generations = %v", deleted)
	}
}
