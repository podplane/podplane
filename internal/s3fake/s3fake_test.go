// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package s3fake

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listBucketResult contains the fields asserted from a ListObjects response.
type listBucketResult struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	IsTruncated bool     `xml:"IsTruncated"`
	MaxKeys     int64    `xml:"MaxKeys"`
	KeyCount    int64    `xml:"KeyCount"`
	Contents    []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	Prefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

// initiateMultipartUploadResult contains an initiated upload's ID.
type initiateMultipartUploadResult struct {
	UploadID string `xml:"UploadId"`
}

// listMultipartUploadsResult contains the uploads returned by the test server.
type listMultipartUploadsResult struct {
	Uploads []struct {
		Key      string `xml:"Key"`
		UploadID string `xml:"UploadId"`
	} `xml:"Upload"`
}

// listMultipartPartsResult contains the parts returned by the test server.
type listMultipartPartsResult struct {
	Parts []struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	} `xml:"Part"`
}

// TestBucketHandlerListRespectsMaxKeys verifies object-list pagination.
func TestBucketHandlerListRespectsMaxKeys(t *testing.T) {
	storageDir := t.TempDir()
	writeTestFile(t, storageDir, "repo/blobs/sha256/a", "a")
	writeTestFile(t, storageDir, "repo/blobs/sha256/b", "b")
	writeTestFile(t, storageDir, "repo/index.json", "{}")

	handler, err := BucketHandler("registry", storageDir)
	if err != nil {
		t.Fatalf("BucketHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	result := getListBucketResult(t, server.URL+"/registry?list-type=2&prefix=repo&max-keys=1")
	if got := len(result.Contents) + len(result.Prefixes); got != 1 {
		t.Fatalf("result count = %d, want 1: %#v", got, result)
	}
	if !result.IsTruncated {
		t.Fatal("IsTruncated = false, want true")
	}
	if result.KeyCount != 1 {
		t.Fatalf("KeyCount = %d, want 1", result.KeyCount)
	}
}

// TestBucketHandlerHeadIncludesLastModifiedForFilesystemFile verifies metadata
// synthesis for files written directly to the cache.
func TestBucketHandlerHeadIncludesLastModifiedForFilesystemFile(t *testing.T) {
	storageDir := t.TempDir()
	path := writeTestFile(t, storageDir, "repo/blobs/sha256/a", "a")
	mtime := time.Date(2026, 6, 11, 5, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	handler, err := BucketHandler("registry", storageDir)
	if err != nil {
		t.Fatalf("BucketHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodHead, server.URL+"/registry/repo/blobs/sha256/a", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Last-Modified"); got == "" {
		t.Fatal("Last-Modified header is empty")
	} else if _, err := time.Parse(http.TimeFormat, got); err != nil {
		t.Fatalf("Last-Modified = %q is not HTTP time: %v", got, err)
	}
}

// TestBucketHandlerPersistsMultipartUploadsAcrossRestart verifies that uploads
// can be resumed, copied, completed, and aborted after replacing the handler.
func TestBucketHandlerPersistsMultipartUploadsAcrossRestart(t *testing.T) {
	storageDir := t.TempDir()
	server := newTestBucketServer(t, storageDir)

	resumedUploadID := initiateMultipartUpload(t, server.URL, "repo/.uploads/resume")
	firstETag := uploadPart(t, server.URL, "repo/.uploads/resume", resumedUploadID, 1, "first")
	abortedUploadID := initiateMultipartUpload(t, server.URL, "repo/.uploads/abort")
	_ = uploadPart(t, server.URL, "repo/.uploads/abort", abortedUploadID, 1, "discarded")
	server.Close()

	server = newTestBucketServer(t, storageDir)
	t.Cleanup(server.Close)
	uploads := listMultipartUploads(t, server.URL)
	if len(uploads.Uploads) != 2 {
		t.Fatalf("uploads after restart = %#v, want 2 uploads", uploads.Uploads)
	}

	parts := listMultipartParts(t, server.URL, "repo/.uploads/resume", resumedUploadID)
	if len(parts.Parts) != 1 || parts.Parts[0].PartNumber != 1 || parts.Parts[0].ETag != firstETag {
		t.Fatalf("parts after restart = %#v, want persisted part 1 with ETag %s", parts.Parts, firstETag)
	}
	secondETag := uploadPart(t, server.URL, "repo/.uploads/resume", resumedUploadID, 2, "second")
	completeMultipartUpload(t, server.URL, "repo/.uploads/resume", resumedUploadID, []multipartCompletionPart{
		{number: 1, etag: firstETag},
		{number: 2, etag: secondETag},
	})
	if got := getObject(t, server.URL, "repo/.uploads/resume"); got != "firstsecond" {
		t.Fatalf("completed object = %q, want %q", got, "firstsecond")
	}
	replacementUploadID := initiateMultipartUpload(t, server.URL, "repo/.uploads/resume")
	copiedETag := copyPart(t, server.URL, "repo/.uploads/resume", replacementUploadID, 1, "repo/.uploads/resume")
	parts = listMultipartParts(t, server.URL, "repo/.uploads/resume", replacementUploadID)
	if len(parts.Parts) != 1 || parts.Parts[0].ETag != copiedETag {
		t.Fatalf("copied parts = %#v, want one part with ETag %s", parts.Parts, copiedETag)
	}
	completeMultipartUpload(t, server.URL, "repo/.uploads/resume", replacementUploadID, []multipartCompletionPart{{number: 1, etag: copiedETag}})
	if got := getObject(t, server.URL, "repo/.uploads/resume"); got != "firstsecond" {
		t.Fatalf("copied object = %q, want %q", got, "firstsecond")
	}

	abortMultipartUpload(t, server.URL, "repo/.uploads/abort", abortedUploadID)
	if uploads := listMultipartUploads(t, server.URL); len(uploads.Uploads) != 0 {
		t.Fatalf("uploads after complete and abort = %#v, want none", uploads.Uploads)
	}
}

// getListBucketResult fetches and decodes a ListObjects response.
func getListBucketResult(t *testing.T, url string) listBucketResult {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var result listBucketResult
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	return result
}

// newTestBucketServer starts a registry bucket server backed by storageDir.
func newTestBucketServer(t *testing.T, storageDir string) *httptest.Server {
	t.Helper()
	handler, err := BucketHandler("registry", storageDir)
	if err != nil {
		t.Fatalf("BucketHandler: %v", err)
	}
	return httptest.NewServer(handler)
}

// initiateMultipartUpload starts an upload and returns its ID.
func initiateMultipartUpload(t *testing.T, serverURL, object string) string {
	t.Helper()
	response := doRequest(t, http.MethodPost, serverURL+"/registry/"+object+"?uploads", nil)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initiate multipart status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var result initiateMultipartUploadResult
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode initiate multipart response: %v", err)
	}
	if result.UploadID == "" {
		t.Fatal("initiate multipart returned an empty upload ID")
	}
	return result.UploadID
}

// uploadPart uploads content and returns the part ETag.
func uploadPart(t *testing.T, serverURL, object, uploadID string, partNumber int, content string) string {
	t.Helper()
	url := serverURL + "/registry/" + object + "?partNumber=" + strconv.Itoa(partNumber) + "&uploadId=" + uploadID
	response := doRequest(t, http.MethodPut, url, strings.NewReader(content))
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("upload part status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if etag := response.Header.Get("ETag"); etag != "" {
		return etag
	}
	t.Fatal("upload part returned an empty ETag")
	return ""
}

// copyPart copies source into an upload part and returns its ETag.
func copyPart(t *testing.T, serverURL, object, uploadID string, partNumber int, source string) string {
	t.Helper()
	url := serverURL + "/registry/" + object + "?partNumber=" + strconv.Itoa(partNumber) + "&uploadId=" + uploadID
	request, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("X-Amz-Copy-Source", "/registry/"+source)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("copy part: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("copy part status = %d, want %d: %s", response.StatusCode, http.StatusOK, responseBody)
	}
	var result struct {
		ETag string `xml:"ETag"`
	}
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode copy part response: %v", err)
	}
	if result.ETag == "" {
		t.Fatal("copy part returned an empty ETag")
	}
	return result.ETag
}

// listMultipartUploads fetches and decodes the active upload list.
func listMultipartUploads(t *testing.T, serverURL string) listMultipartUploadsResult {
	t.Helper()
	response := doRequest(t, http.MethodGet, serverURL+"/registry?uploads", nil)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list multipart uploads status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var result listMultipartUploadsResult
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode list multipart uploads response: %v", err)
	}
	return result
}

// listMultipartParts fetches and decodes an upload's parts.
func listMultipartParts(t *testing.T, serverURL, object, uploadID string) listMultipartPartsResult {
	t.Helper()
	response := doRequest(t, http.MethodGet, serverURL+"/registry/"+object+"?uploadId="+uploadID, nil)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list multipart parts status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var result listMultipartPartsResult
	if err := xml.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode list multipart parts response: %v", err)
	}
	return result
}

// multipartCompletionPart identifies a part included in a completion request.
type multipartCompletionPart struct {
	number int
	etag   string
}

// completeMultipartUpload completes an upload from parts.
func completeMultipartUpload(t *testing.T, serverURL, object, uploadID string, parts []multipartCompletionPart) {
	t.Helper()
	var body bytes.Buffer
	body.WriteString("<CompleteMultipartUpload>")
	for _, part := range parts {
		_, _ = fmt.Fprintf(&body, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", part.number, part.etag)
	}
	body.WriteString("</CompleteMultipartUpload>")
	response := doRequest(t, http.MethodPost, serverURL+"/registry/"+object+"?uploadId="+uploadID, &body)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("complete multipart status = %d, want %d: %s", response.StatusCode, http.StatusOK, responseBody)
	}
}

// abortMultipartUpload aborts an active upload.
func abortMultipartUpload(t *testing.T, serverURL, object, uploadID string) {
	t.Helper()
	response := doRequest(t, http.MethodDelete, serverURL+"/registry/"+object+"?uploadId="+uploadID, nil)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("abort multipart status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

// getObject fetches an object and returns its contents.
func getObject(t *testing.T, serverURL, object string) string {
	t.Helper()
	response := doRequest(t, http.MethodGet, serverURL+"/registry/"+object, nil)
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get object status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	return string(data)
}

// doRequest builds and sends an HTTP request, failing the test on error.
func doRequest(t *testing.T, method, url string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return response
}

// writeTestFile writes content beneath root and returns the file path.
func writeTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
