// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestStateLock verifies exclusion, version cleanup, and ownership-safe release.
func TestStateLock(t *testing.T) {
	const key = "podplane/cluster.tfstate.tflock"
	var mu sync.Mutex
	versions := map[string]string{"old": key, "data": "state", "marker": key}
	live, serial := "", 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "PUT":
			if live != "" && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = fmt.Fprint(w, `<Error><Code>PreconditionFailed</Code></Error>`)
				return
			}
			serial++
			live = fmt.Sprint(serial)
			versions[live] = key
			w.Header().Set("ETag", `"`+live+`"`)
			w.Header().Set("x-amz-version-id", live)
		case r.Method == "GET" && r.URL.Query().Has("versions"):
			_, _ = fmt.Fprint(w, `<ListVersionsResult>`)
			for id, name := range versions {
				tag := "Version"
				if id == "marker" {
					tag = "DeleteMarker"
				}
				_, _ = fmt.Fprintf(w, `<%s><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest></%s>`, tag, name, id, id == live, tag)
			}
			_, _ = fmt.Fprint(w, `</ListVersionsResult>`)
		case r.Method == "GET" && r.URL.Query().Has("uploads"):
			_, _ = fmt.Fprint(w, `<ListMultipartUploadsResult/>`)
		case r.Method == "POST" && r.URL.Query().Has("delete"):
			var request struct {
				Objects []struct{ Key, VersionId string } `xml:"Object"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			for _, object := range request.Objects {
				delete(versions, object.VersionId)
				if object.VersionId == live {
					live = ""
				}
			}
			_, _ = fmt.Fprint(w, `<DeleteResult/>`)
		case r.Method == "DELETE":
			if match := r.Header.Get("If-Match"); match != "" && match != `"`+live+`"` {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = fmt.Fprint(w, `<Error><Code>PreconditionFailed</Code></Error>`)
				return
			}
			if id := r.URL.Query().Get("versionId"); id != "" {
				delete(versions, id)
			} else {
				versions["marker"] = key
			}
			live = ""
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	p := &Provider{s3: s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}}, func(o *s3.Options) {
		o.BaseEndpoint, o.UsePathStyle = aws.String(server.URL), true
	})}
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
	remaining, held := len(versions), live != ""
	mu.Unlock()
	if remaining != 1 || !held {
		t.Fatalf("cleanup must retain only the live lock: versions=%d, held=%t", remaining, held)
	}
	if err := lock.Unlock(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	remaining = len(versions)
	mu.Unlock()
	if remaining != 0 {
		t.Fatal("lock release left a version or delete marker")
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
		t.Fatal("stale owner removed the replacement lock")
	}
}
