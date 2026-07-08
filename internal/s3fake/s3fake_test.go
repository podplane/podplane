// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

package s3fake

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
