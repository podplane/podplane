// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// TestStateLock checks conditional acquisition, retained generations, and stale owners.
func TestStateLock(t *testing.T) {
	const key = "podplane/cluster/default.tflock"
	var mu sync.Mutex
	objects := map[string]string{"1": key, "2": "state"}
	live, serial := "", 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			if live != "" && r.URL.Query().Get("ifGenerationMatch") == "0" {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = fmt.Fprint(w, `{"error":{"code":412,"message":"already locked"}}`)
				return
			}
			serial++
			live = fmt.Sprint(serial)
			objects[live] = key
			_, _ = fmt.Fprintf(w, `{"name":%q,"generation":%q}`, key, live)
		case "GET":
			var items []map[string]string
			for generation, name := range objects {
				items = append(items, map[string]string{"name": name, "generation": generation})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case "DELETE":
			generation := r.URL.Query().Get("generation")
			if match := r.URL.Query().Get("ifGenerationMatch"); match != "" && match != live {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = fmt.Fprint(w, `{"error":{"code":412,"message":"lock replaced"}}`)
				return
			}
			if generation != "" {
				delete(objects, generation)
			}
			if generation == "" || generation == live {
				live = ""
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
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
	lock, err := p.LockState(t.Context(), "test", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LockState(t.Context(), "test", key); err == nil {
		t.Fatal("second client acquired a held lock")
	}
	if err := lock.EmptyBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	remaining, held := len(objects), live != ""
	mu.Unlock()
	if remaining != 1 || !held {
		t.Fatalf("cleanup must retain only the live lock: objects=%d, held=%t", remaining, held)
	}
	if err := lock.Unlock(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	remaining = len(objects)
	mu.Unlock()
	if remaining != 0 {
		t.Fatal("release archived the lock instead of removing its generation")
	}
	old, err := p.LockState(t.Context(), "test", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Unlock(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LockState(t.Context(), "test", key); err != nil {
		t.Fatal(err)
	}
	if err := old.Unlock(t.Context()); err == nil {
		t.Fatal("stale owner removed a replacement lock")
	}
}
