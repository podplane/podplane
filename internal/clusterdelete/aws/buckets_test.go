// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestEmptyBucket checks version and upload cleanup, including per-object failures.
func TestEmptyBucket(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(fmt.Sprint(deny), func(t *testing.T) {
			versions, upload := true, true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == "GET" && r.URL.Query().Has("versions"):
					if versions {
						_, _ = fmt.Fprint(w, `<ListVersionsResult><Version><Key>data</Key><VersionId>v1</VersionId></Version><Version><Key>data</Key><VersionId>v2</VersionId></Version><DeleteMarker><Key>data</Key><VersionId>marker</VersionId></DeleteMarker></ListVersionsResult>`)
					} else {
						_, _ = fmt.Fprint(w, `<ListVersionsResult/>`)
					}
				case r.Method == "POST" && r.URL.Query().Has("delete"):
					var request struct {
						Objects []struct{ Key, VersionId string } `xml:"Object"`
					}
					if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if len(request.Objects) != 3 || request.Objects[0].VersionId != "v1" || request.Objects[1].VersionId != "v2" || request.Objects[2].VersionId != "marker" {
						t.Error("versions or markers missing from delete")
					}
					if deny {
						_, _ = fmt.Fprint(w, `<DeleteResult><Error><Key>data</Key><Code>AccessDenied</Code><Message>locked</Message></Error></DeleteResult>`)
					} else {
						versions = false
						_, _ = fmt.Fprint(w, `<DeleteResult/>`)
					}
				case r.Method == "GET" && r.URL.Query().Has("uploads"):
					if upload {
						_, _ = fmt.Fprint(w, `<ListMultipartUploadsResult><Upload><Key>pending</Key><UploadId>upload-1</UploadId></Upload></ListMultipartUploadsResult>`)
					} else {
						_, _ = fmt.Fprint(w, `<ListMultipartUploadsResult/>`)
					}
				case r.Method == "DELETE" && r.URL.Query().Get("uploadId") == "upload-1":
					upload = false
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			p := &Provider{s3: s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}}, func(o *s3.Options) { o.BaseEndpoint = aws.String(server.URL); o.UsePathStyle = true })}
			err := p.EmptyBucket(t.Context(), "test-bucket")
			if deny {
				if err == nil || !versions || !upload {
					t.Fatalf("object error did not stop cleanup: %v", err)
				}
			} else if err != nil || versions || upload {
				t.Fatalf("bucket not emptied: %v", err)
			}
		})
	}
}
