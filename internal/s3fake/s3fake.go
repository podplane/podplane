// Podplane <https://podplane.dev>
// Copyright The Podplane Authors
// SPDX-License-Identifier: Apache-2.0

// Package s3fake exposes a lightweight, on-disk fake S3 server suitable for
// embedding in the local Podplane background server.
//
// It wraps github.com/johannesboyne/gofakes3 with the s3afero MultiBucket
// backend so tests / dev clusters can read and write a normal filesystem
// directory as if it were S3.
package s3fake

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3afero"
	"github.com/spf13/afero"
)

// Handler returns an S3-compatible multi-bucket API backed by the supplied
// storage directory. Buckets are created on first use.
//
// storageDir must exist (or be creatable). If it does not exist it is created
// with mode 0700.
func Handler(storageDir string) (http.Handler, error) {
	if storageDir == "" {
		return nil, fmt.Errorf("s3fake: storageDir is required")
	}
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, fmt.Errorf("s3fake: mkdir storage: %w", err)
	}

	baseFs := afero.NewBasePathFs(afero.NewOsFs(), storageDir)
	backend, err := s3afero.MultiBucket(baseFs)
	if err != nil {
		return nil, fmt.Errorf("s3fake: build backend: %w", err)
	}
	faker := gofakes3.New(
		backend,
		gofakes3.WithAutoBucket(true),
		gofakes3.WithLogger(&renamingLogger{inner: gofakes3.GlobalLog()}),
	)
	return faker.Server(), nil
}

// WriteObject writes one object directly to the fake S3 on-disk storage using
// the same gofakes3 backend as Handler. It is useful for seeding local buckets
// before the HTTP server is running while still preserving backend metadata.
func WriteObject(storageDir, bucketName, objectName string, data []byte) error {
	if storageDir == "" {
		return fmt.Errorf("s3fake: storageDir is required")
	}
	if bucketName == "" {
		return fmt.Errorf("s3fake: bucketName is required")
	}
	if objectName == "" {
		return fmt.Errorf("s3fake: objectName is required")
	}
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return fmt.Errorf("s3fake: mkdir storage: %w", err)
	}

	baseFs := afero.NewBasePathFs(afero.NewOsFs(), storageDir)
	backend, err := s3afero.MultiBucket(baseFs)
	if err != nil {
		return fmt.Errorf("s3fake: build backend: %w", err)
	}
	if err := backend.CreateBucket(bucketName); err != nil && !gofakes3.IsAlreadyExists(err) {
		return fmt.Errorf("s3fake: create bucket %s: %w", bucketName, err)
	}
	if _, err := backend.PutObject(bucketName, objectName, nil, bytes.NewReader(data), int64(len(data)), nil); err != nil {
		return fmt.Errorf("s3fake: write object %s/%s: %w", bucketName, objectName, err)
	}
	return nil
}

// BucketHandler returns an S3-compatible single-bucket API backed by storageDir.
func BucketHandler(bucketName, storageDir string) (http.Handler, error) {
	if bucketName == "" {
		return nil, fmt.Errorf("s3fake: bucketName is required")
	}
	if storageDir == "" {
		return nil, fmt.Errorf("s3fake: storageDir is required")
	}
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, fmt.Errorf("s3fake: mkdir storage: %w", err)
	}

	baseFs := afero.NewBasePathFs(afero.NewOsFs(), storageDir)
	backend, err := s3afero.SingleBucket(bucketName, baseFs, afero.NewMemMapFs())
	if err != nil {
		return nil, fmt.Errorf("s3fake: build bucket backend: %w", err)
	}
	multipartDir := storageDir + ".multipart"
	if err := os.MkdirAll(multipartDir, 0700); err != nil {
		return nil, fmt.Errorf("s3fake: mkdir multipart storage: %w", err)
	}
	wrappedBackend := &cacheBucketBackend{Backend: backend, fs: baseFs, multipartDir: multipartDir}
	faker := gofakes3.New(
		wrappedBackend,
		gofakes3.WithLogger(&renamingLogger{inner: gofakes3.GlobalLog()}),
	)
	return &multipartCopyHandler{next: faker.Server(), backend: wrappedBackend}, nil
}

// multipartCopyHandler supplies the UploadPartCopy operation used by the
// Distribution S3 driver when it resumes a multipart upload. gofakes3 routes
// these bodyless PUT requests as regular UploadPart calls.
type multipartCopyHandler struct {
	next    http.Handler
	backend *cacheBucketBackend
}

// ServeHTTP handles UploadPartCopy requests and delegates all other requests
// to gofakes3.
func (h *multipartCopyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	copySource := request.Header.Get("X-Amz-Copy-Source")
	if request.Method != http.MethodPut || copySource == "" || query.Get("uploadId") == "" || query.Get("partNumber") == "" {
		h.next.ServeHTTP(response, request)
		return
	}
	partNumber, err := strconv.Atoi(query.Get("partNumber"))
	if err != nil || partNumber <= 0 || request.Header.Get("X-Amz-Copy-Source-Range") != "" {
		h.next.ServeHTTP(response, request)
		return
	}
	source, err := url.PathUnescape(strings.SplitN(copySource, "?", 2)[0])
	if err != nil {
		h.next.ServeHTTP(response, request)
		return
	}
	sourceBucket, sourceObject, found := strings.Cut(strings.TrimPrefix(source, "/"), "/")
	destinationBucket, destinationObject, foundDestination := strings.Cut(strings.TrimPrefix(request.URL.Path, "/"), "/")
	if !found || !foundDestination {
		h.next.ServeHTTP(response, request)
		return
	}
	object, err := h.backend.GetObject(sourceBucket, sourceObject, nil)
	if err != nil {
		h.next.ServeHTTP(response, request)
		return
	}
	defer func() { _ = object.Contents.Close() }()
	etag, err := h.backend.UploadPart(
		destinationBucket,
		destinationObject,
		gofakes3.UploadID(query.Get("uploadId")),
		partNumber,
		object.Size,
		object.Contents,
	)
	if err != nil {
		h.next.ServeHTTP(response, request)
		return
	}
	response.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(response).Encode(struct {
		XMLName      xml.Name             `xml:"CopyPartResult"`
		LastModified gofakes3.ContentTime `xml:"LastModified"`
		ETag         string               `xml:"ETag"`
	}{
		LastModified: gofakes3.NewContentTime(time.Now()),
		ETag:         etag,
	})
}

// cacheBucketBackend adapts gofakes3's filesystem single-bucket backend for
// cache directories that are populated directly on disk rather than via S3 PUT.
type cacheBucketBackend struct {
	gofakes3.Backend
	fs           afero.Fs
	multipartDir string
	multipartMu  sync.Mutex
}

var _ gofakes3.MultipartBackend = (*cacheBucketBackend)(nil)

// persistentMultipartUpload is the durable control record for an upload.
type persistentMultipartUpload struct {
	Bucket    string            `json:"bucket"`
	Object    string            `json:"object"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Initiated time.Time         `json:"initiated"`
}

// cacheBucketListItem is one sortable result item from a ListBucket response.
type cacheBucketListItem struct {
	key     string
	content *gofakes3.Content
	prefix  *gofakes3.CommonPrefix
}

// ListBucket adds paging support to s3afero.SingleBucket, which otherwise asks
// gofakes3 to retry list requests without max-keys/marker handling.
func (b *cacheBucketBackend) ListBucket(name string, prefix *gofakes3.Prefix, page gofakes3.ListBucketPage) (*gofakes3.ObjectList, error) {
	objects, err := b.Backend.ListBucket(name, prefix, gofakes3.ListBucketPage{})
	if err != nil {
		return nil, err
	}
	return applyListBucketPage(objects, page), nil
}

// HeadObject ensures filesystem-backed objects have Last-Modified metadata.
func (b *cacheBucketBackend) HeadObject(bucketName, objectName string) (*gofakes3.Object, error) {
	object, err := b.Backend.HeadObject(bucketName, objectName)
	if err != nil {
		return nil, err
	}
	b.decorateObjectMetadata(object)
	return object, nil
}

// GetObject ensures filesystem-backed objects have Last-Modified metadata.
func (b *cacheBucketBackend) GetObject(bucketName, objectName string, rangeRequest *gofakes3.ObjectRangeRequest) (*gofakes3.Object, error) {
	object, err := b.Backend.GetObject(bucketName, objectName, rangeRequest)
	if err != nil {
		return nil, err
	}
	b.decorateObjectMetadata(object)
	return object, nil
}

// CreateMultipartUpload stores upload control state alongside the cache so it
// survives local server restarts. The state directory is adjacent to, rather
// than inside, the object directory to keep it out of S3 object listings.
func (b *cacheBucketBackend) CreateMultipartUpload(bucket, object string, metadata map[string]string) (gofakes3.UploadID, error) {
	b.multipartMu.Lock()
	defer b.multipartMu.Unlock()

	for {
		id := gofakes3.UploadID(rand.Text())
		dir := b.multipartUploadDir(id)
		if err := os.Mkdir(dir, 0700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		upload := persistentMultipartUpload{
			Bucket:    bucket,
			Object:    object,
			Metadata:  metadata,
			Initiated: time.Now().UTC(),
		}
		data, err := json.Marshal(upload)
		temporary := filepath.Join(dir, "upload.json.tmp")
		if err == nil {
			err = os.WriteFile(temporary, data, 0600)
		}
		if err == nil {
			err = os.Rename(temporary, filepath.Join(dir, "upload.json"))
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		return id, nil
	}
}

// UploadPart atomically replaces a persisted upload part.
func (b *cacheBucketBackend) UploadPart(bucket, object string, id gofakes3.UploadID, partNumber int, contentLength int64, input io.Reader) (string, error) {
	if partNumber <= 0 || partNumber > gofakes3.MaxUploadPartNumber || contentLength < 0 {
		return "", gofakes3.ErrInvalidPart
	}
	b.multipartMu.Lock()
	defer b.multipartMu.Unlock()

	if _, err := b.multipartUpload(bucket, object, id); err != nil {
		return "", err
	}
	dir := b.multipartUploadDir(id)
	part := filepath.Join(dir, multipartPartName(partNumber))
	file, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	hash := md5.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, contentLength+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != contentLength {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return "", gofakes3.ErrIncompleteBody
	}
	if err := os.Rename(temporary, part); err != nil {
		_ = os.Remove(temporary)
		return "", err
	}
	return fmt.Sprintf(`"%x"`, hash.Sum(nil)), nil
}

// ListMultipartUploads returns persisted in-progress uploads.
func (b *cacheBucketBackend) ListMultipartUploads(bucket string, marker *gofakes3.UploadListMarker, prefix gofakes3.Prefix, limit int64) (*gofakes3.ListMultipartUploadsResult, error) {
	b.multipartMu.Lock()
	defer b.multipartMu.Unlock()

	uploads, err := b.multipartUploads(bucket)
	if err != nil {
		return nil, err
	}
	result := &gofakes3.ListMultipartUploadsResult{
		Bucket:     bucket,
		Delimiter:  prefix.Delimiter,
		Prefix:     prefix.Prefix,
		MaxUploads: limit,
	}
	if marker != nil {
		result.KeyMarker = marker.Object
		result.UploadIDMarker = marker.UploadID
	}

	seenPrefixes := map[string]bool{}
	var matchedPrefix gofakes3.PrefixMatch
	pastMarker := marker == nil
	for _, upload := range uploads {
		if !pastMarker {
			switch {
			case upload.Object < marker.Object:
				continue
			case upload.Object > marker.Object:
				pastMarker = true
			case marker.UploadID == "":
				continue
			case upload.ID == marker.UploadID:
				pastMarker = true
				continue
			default:
				continue
			}
		}
		if !prefix.Match(upload.Object, &matchedPrefix) {
			continue
		}
		if matchedPrefix.CommonPrefix {
			if !seenPrefixes[matchedPrefix.MatchedPart] {
				result.CommonPrefixes = append(result.CommonPrefixes, matchedPrefix.AsCommonPrefix())
				seenPrefixes[matchedPrefix.MatchedPart] = true
			}
			continue
		}
		if int64(len(result.Uploads)) >= limit {
			result.IsTruncated = true
			break
		}
		result.Uploads = append(result.Uploads, gofakes3.ListMultipartUploadItem{
			Key:          upload.Object,
			UploadID:     upload.ID,
			StorageClass: "STANDARD",
			Initiated:    gofakes3.NewContentTime(upload.Initiated),
		})
	}
	if result.IsTruncated && len(result.Uploads) > 0 {
		last := result.Uploads[len(result.Uploads)-1]
		result.NextKeyMarker = last.Key
		result.NextUploadIDMarker = last.UploadID
	}
	return result, nil
}

// ListParts returns the persisted parts for an upload in part-number order.
func (b *cacheBucketBackend) ListParts(bucket, object string, id gofakes3.UploadID, marker int, limit int64) (*gofakes3.ListMultipartUploadPartsResult, error) {
	b.multipartMu.Lock()
	defer b.multipartMu.Unlock()

	if _, err := b.multipartUpload(bucket, object, id); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(b.multipartUploadDir(id))
	if err != nil {
		return nil, err
	}
	partNumbers := make([]int, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".part") {
			continue
		}
		partNumber, err := strconv.Atoi(strings.TrimSuffix(name, ".part"))
		if err == nil && partNumber > 0 {
			partNumbers = append(partNumbers, partNumber)
		}
	}
	sort.Ints(partNumbers)
	result := &gofakes3.ListMultipartUploadPartsResult{
		Bucket:           bucket,
		Key:              object,
		UploadID:         id,
		StorageClass:     "STANDARD",
		PartNumberMarker: marker,
		MaxParts:         limit,
	}
	for _, partNumber := range partNumbers {
		if partNumber <= marker {
			continue
		}
		if int64(len(result.Parts)) >= limit {
			result.IsTruncated = true
			break
		}
		path := filepath.Join(b.multipartUploadDir(id), multipartPartName(partNumber))
		etag, _, size, modified, err := multipartPartInfo(path)
		if err != nil {
			return nil, err
		}
		result.Parts = append(result.Parts, gofakes3.ListMultipartUploadPartItem{
			PartNumber:   partNumber,
			ETag:         etag,
			Size:         size,
			LastModified: gofakes3.NewContentTime(modified),
		})
	}
	if result.IsTruncated && len(result.Parts) > 0 {
		result.NextPartNumberMarker = result.Parts[len(result.Parts)-1].PartNumber
	}
	return result, nil
}

// AbortMultipartUpload deletes all persisted state and parts for an upload.
func (b *cacheBucketBackend) AbortMultipartUpload(bucket, object string, id gofakes3.UploadID) error {
	b.multipartMu.Lock()
	defer b.multipartMu.Unlock()
	if _, err := b.multipartUpload(bucket, object, id); err != nil {
		return err
	}
	return os.RemoveAll(b.multipartUploadDir(id))
}

// CompleteMultipartUpload assembles persisted parts into the final object and
// only removes upload state after the object has been stored successfully.
func (b *cacheBucketBackend) CompleteMultipartUpload(bucket, object string, id gofakes3.UploadID, input *gofakes3.CompleteMultipartUploadRequest) (gofakes3.VersionID, string, error) {
	b.multipartMu.Lock()
	defer b.multipartMu.Unlock()

	upload, err := b.multipartUpload(bucket, object, id)
	if err != nil {
		return "", "", err
	}
	for i, part := range input.Parts {
		if part.PartNumber <= 0 || (i > 0 && input.Parts[i-1].PartNumber >= part.PartNumber) {
			return "", "", gofakes3.ErrInvalidPartOrder
		}
	}

	partPaths := make([]string, 0, len(input.Parts))
	multipartHash := md5.New()
	var totalSize int64
	for _, requestedPart := range input.Parts {
		path := filepath.Join(b.multipartUploadDir(id), multipartPartName(requestedPart.PartNumber))
		etag, digest, size, _, err := multipartPartInfo(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", "", gofakes3.ErrInvalidPart
			}
			return "", "", err
		}
		if strings.Trim(etag, "\"") != strings.Trim(requestedPart.ETag, "\"") {
			return "", "", gofakes3.ErrInvalidPart
		}
		partPaths = append(partPaths, path)
		_, _ = multipartHash.Write(digest)
		totalSize += size
	}

	reader := &multipartPartsReader{paths: partPaths}
	defer func() { _ = reader.Close() }()
	putResult, err := b.PutObject(bucket, object, upload.Metadata, reader, totalSize, nil)
	if err != nil {
		return "", "", err
	}
	activeDir := b.multipartUploadDir(id)
	completedDir := filepath.Join(b.multipartDir, ".completed-"+string(id))
	if err := os.Rename(activeDir, completedDir); err == nil {
		_ = os.RemoveAll(completedDir)
	} else {
		_ = os.RemoveAll(activeDir)
	}
	etag := fmt.Sprintf(`"%s-%d"`, hex.EncodeToString(multipartHash.Sum(nil)), len(input.Parts))
	return putResult.VersionID, etag, nil
}

// multipartPartsReader streams a sequence of files as one reader.
type multipartPartsReader struct {
	paths   []string
	current *os.File
}

// Read reads the persisted parts in order, opening at most one part at a time.
func (r *multipartPartsReader) Read(data []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.paths) == 0 {
				return 0, io.EOF
			}
			file, err := os.Open(r.paths[0])
			if err != nil {
				return 0, err
			}
			r.paths = r.paths[1:]
			r.current = file
		}
		read, err := r.current.Read(data)
		if err != io.EOF {
			return read, err
		}
		if closeErr := r.current.Close(); closeErr != nil {
			return read, closeErr
		}
		r.current = nil
		if read > 0 {
			return read, nil
		}
	}
}

// Close closes the part currently being read, if any.
func (r *multipartPartsReader) Close() error {
	if r.current == nil {
		return nil
	}
	err := r.current.Close()
	r.current = nil
	return err
}

// listedMultipartUpload associates a persisted record with its upload ID.
type listedMultipartUpload struct {
	persistentMultipartUpload
	ID gofakes3.UploadID
}

// multipartUploads loads and orders the persisted uploads for bucket.
func (b *cacheBucketBackend) multipartUploads(bucket string) ([]listedMultipartUpload, error) {
	entries, err := os.ReadDir(b.multipartDir)
	if err != nil {
		return nil, err
	}
	uploads := make([]listedMultipartUpload, 0, len(entries))
	for _, entry := range entries {
		id := gofakes3.UploadID(entry.Name())
		if !entry.IsDir() || !validMultipartUploadID(id) {
			continue
		}
		upload, err := b.readMultipartUpload(id)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if upload.Bucket == bucket {
			uploads = append(uploads, listedMultipartUpload{persistentMultipartUpload: upload, ID: id})
		}
	}
	sort.Slice(uploads, func(i, j int) bool {
		if uploads[i].Object == uploads[j].Object {
			if uploads[i].Initiated.Equal(uploads[j].Initiated) {
				return uploads[i].ID < uploads[j].ID
			}
			return uploads[i].Initiated.Before(uploads[j].Initiated)
		}
		return uploads[i].Object < uploads[j].Object
	})
	return uploads, nil
}

// multipartUpload loads id and verifies that it belongs to bucket and object.
func (b *cacheBucketBackend) multipartUpload(bucket, object string, id gofakes3.UploadID) (persistentMultipartUpload, error) {
	upload, err := b.readMultipartUpload(id)
	if err != nil {
		if os.IsNotExist(err) {
			return persistentMultipartUpload{}, gofakes3.ErrNoSuchUpload
		}
		return persistentMultipartUpload{}, err
	}
	if upload.Bucket != bucket || upload.Object != object {
		return persistentMultipartUpload{}, gofakes3.ErrNoSuchUpload
	}
	return upload, nil
}

// readMultipartUpload reads a durable upload record from disk.
func (b *cacheBucketBackend) readMultipartUpload(id gofakes3.UploadID) (persistentMultipartUpload, error) {
	if !validMultipartUploadID(id) {
		return persistentMultipartUpload{}, gofakes3.ErrNoSuchUpload
	}
	data, err := os.ReadFile(filepath.Join(b.multipartUploadDir(id), "upload.json"))
	if err != nil {
		return persistentMultipartUpload{}, err
	}
	var upload persistentMultipartUpload
	if err := json.Unmarshal(data, &upload); err != nil {
		return persistentMultipartUpload{}, err
	}
	return upload, nil
}

// multipartUploadDir returns the private directory for id.
func (b *cacheBucketBackend) multipartUploadDir(id gofakes3.UploadID) string {
	return filepath.Join(b.multipartDir, string(id))
}

// validMultipartUploadID reports whether id could have been produced by
// crypto/rand.Text. Restricting the alphabet also prevents path traversal.
func validMultipartUploadID(id gofakes3.UploadID) bool {
	value := string(id)
	return value != "" && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == ""
}

// multipartPartName returns the stable filename for partNumber.
func multipartPartName(partNumber int) string {
	return fmt.Sprintf("%05d.part", partNumber)
}

// multipartPartInfo computes the S3 metadata for a persisted part.
func multipartPartInfo(path string) (etag string, digest []byte, size int64, modified time.Time, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, 0, time.Time{}, err
	}
	defer func() { _ = file.Close() }()
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", nil, 0, time.Time{}, err
	}
	stat, err := file.Stat()
	if err != nil {
		return "", nil, 0, time.Time{}, err
	}
	digest = hash.Sum(nil)
	return fmt.Sprintf(`"%x"`, digest), digest, stat.Size(), stat.ModTime(), nil
}

// decorateObjectMetadata backfills Last-Modified for direct filesystem files.
func (b *cacheBucketBackend) decorateObjectMetadata(object *gofakes3.Object) {
	if object == nil || object.Metadata["Last-Modified"] != "" {
		return
	}
	if object.Metadata == nil {
		object.Metadata = map[string]string{}
	}
	stat, err := b.fs.Stat(filepath.FromSlash(object.Name))
	if err != nil {
		return
	}
	object.Metadata["Last-Modified"] = stat.ModTime().UTC().Format(http.TimeFormat)
}

// applyListBucketPage applies marker/max-keys pagination to a full object list.
func applyListBucketPage(objects *gofakes3.ObjectList, page gofakes3.ListBucketPage) *gofakes3.ObjectList {
	if objects == nil || page.IsEmpty() {
		return objects
	}
	items := make([]cacheBucketListItem, 0, len(objects.Contents)+len(objects.CommonPrefixes))
	for _, content := range objects.Contents {
		if content != nil {
			items = append(items, cacheBucketListItem{key: content.Key, content: content})
		}
	}
	for i := range objects.CommonPrefixes {
		prefix := objects.CommonPrefixes[i]
		items = append(items, cacheBucketListItem{key: prefix.Prefix, prefix: &prefix})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })

	paged := gofakes3.NewObjectList()
	var count int64
	for _, item := range items {
		if page.HasMarker && item.key <= page.Marker {
			continue
		}
		if page.MaxKeys > 0 && count >= page.MaxKeys {
			paged.IsTruncated = true
			break
		}
		if item.content != nil {
			paged.Add(item.content)
		} else if item.prefix != nil {
			paged.AddPrefix(item.prefix.Prefix)
		}
		paged.NextMarker = item.key
		count++
	}
	return paged
}

// renamingLogger wraps a gofakes3.Logger to enable log rewriting.
type renamingLogger struct {
	inner gofakes3.Logger
}

// Print rewrites logs before forwarding to the underlying gofakes3 logger.
// Currently it:
//   - Rewrites the slightly misleading "CREATE OBJECT" log message that the
//     library prints for object PUTs into the more familiar "PUT OBJECT".
func (l *renamingLogger) Print(level gofakes3.LogLevel, v ...any) {
	rewritten := make([]any, len(v))
	for i, item := range v {
		if s, ok := item.(string); ok {
			rewritten[i] = strings.ReplaceAll(s, "CREATE OBJECT", "PUT OBJECT")
		} else {
			rewritten[i] = item
		}
	}
	l.inner.Print(level, rewritten...)
}
