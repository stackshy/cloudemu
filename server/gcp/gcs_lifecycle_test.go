// Package gcp_test —  suite cell STORAGE / gcp / sdk-compat.
//
// These tests drive the REAL cloud.google.com/go/storage SDK against the
// emulator's GCP HTTP server (httptest), asserting on SDK-decoded responses
// and SDK-visible error types. They intentionally exercise full user
// journeys (bucket lifecycle, uploads of varied shapes, listing with
// prefix/delimiter/pagination, copy, deletes) plus edge cases (typed
// not-found errors, duplicate buckets, non-empty bucket deletion, unusual
// key names) and provider-specific behaviors from the suite survey
// (sha256 ETags, copy preserves ETag, versioning being driver-only).
package gcp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const e2eProject = "e2e-project"

// newStorageClient boots a fresh emulator + GCP server per test and
// returns a real GCS SDK client pointed at it. Retries are disabled so
// error-path assertions fail fast instead of backing off on 5xx.
func newStorageClient(t *testing.T) (context.Context, *storage.Client) {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Storage: cloudP.GCS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	// The Go GCS SDK appends /b/... directly to the endpoint, so the
	// /storage/v1/ suffix is required.
	client, err := storage.NewClient(ctx,
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}

	client.SetRetry(storage.WithPolicy(storage.RetryNever))
	t.Cleanup(func() { _ = client.Close() })

	return ctx, client
}

func mustCreateBucket(t *testing.T, ctx context.Context, client *storage.Client, name string) *storage.BucketHandle {
	t.Helper()

	b := client.Bucket(name)
	if err := b.Create(ctx, e2eProject, nil); err != nil {
		t.Fatalf("Create bucket %q: %v", name, err)
	}

	return b
}

func putObject(t *testing.T, ctx context.Context, bkt *storage.BucketHandle, key, contentType string, data []byte, md map[string]string) {
	t.Helper()

	w := bkt.Object(key).NewWriter(ctx)
	w.ContentType = contentType
	w.Metadata = md

	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write %q: %v", key, err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Writer.Close %q: %v", key, err)
	}
}

func readObject(t *testing.T, ctx context.Context, bkt *storage.BucketHandle, key string) []byte {
	t.Helper()

	rd, err := bkt.Object(key).NewReader(ctx)
	if err != nil {
		t.Fatalf("NewReader %q: %v", key, err)
	}
	defer rd.Close()

	got, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("ReadAll %q: %v", key, err)
	}

	return got
}

func listAll(t *testing.T, ctx context.Context, bkt *storage.BucketHandle, q *storage.Query) (keys, prefixes []string) {
	t.Helper()

	it := bkt.Objects(ctx, q)

	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("Objects iterator: %v", err)
		}

		if a.Prefix != "" {
			prefixes = append(prefixes, a.Prefix)
			continue
		}

		keys = append(keys, a.Name)
	}

	return keys, prefixes
}

// isSDKNotFound reports whether err is one of the SDK's typed not-found
// shapes: storage.ErrObjectNotExist / storage.ErrBucketNotExist, or a
// *googleapi.Error with HTTP 404.
func isSDKNotFound(err error) bool {
	if errors.Is(err, storage.ErrObjectNotExist) || errors.Is(err, storage.ErrBucketNotExist) {
		return true
	}

	var gerr *googleapi.Error

	return errors.As(err, &gerr) && gerr.Code == http.StatusNotFound
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// TestStorageFullLifecycle covers the primary user journey:
// create bucket -> upload objects of varied content types and sizes
// (including empty and ~1MB) -> stat -> download -> list -> copy across
// buckets -> delete objects -> delete buckets.
func TestStorageFullLifecycle(t *testing.T) {
	ctx, client := newStorageClient(t)

	src := mustCreateBucket(t, ctx, client, "e2e-lifecycle-src")
	dst := mustCreateBucket(t, ctx, client, "e2e-lifecycle-dst")

	// ListBuckets: both buckets visible, sorted by name.
	bit := client.Buckets(ctx, e2eProject)

	var bucketNames []string

	for {
		b, err := bit.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("Buckets iterator: %v", err)
		}

		bucketNames = append(bucketNames, b.Name)
	}

	wantBuckets := []string{"e2e-lifecycle-dst", "e2e-lifecycle-src"}
	if len(bucketNames) != len(wantBuckets) || bucketNames[0] != wantBuckets[0] || bucketNames[1] != wantBuckets[1] {
		t.Errorf("ListBuckets = %v, want %v (sorted)", bucketNames, wantBuckets)
	}

	// Bucket stat via Attrs.
	battrs, err := src.Attrs(ctx)
	if err != nil {
		t.Fatalf("bucket Attrs: %v", err)
	}

	if battrs.Name != "e2e-lifecycle-src" {
		t.Errorf("bucket Attrs.Name = %q", battrs.Name)
	}

	// Varied payloads: text with metadata, empty object, ~1MB binary.
	textBody := []byte("hello, storage e2e")
	empty := []byte{}

	big := make([]byte, 1<<20)
	for i := range big {
		big[i] = byte(i % 251)
	}
	// Avoid trailing '-', '\r', '\n' bytes here; that edge is covered (and
	// currently broken) in TestStorageTrailingBoundaryBytes.
	big[len(big)-1] = 'Z'

	putObject(t, ctx, src, "docs/hello.txt", "text/plain", textBody, map[string]string{"team": "e2e", "run": "suite"})
	putObject(t, ctx, src, "empty.bin", "application/octet-stream", empty, nil)
	putObject(t, ctx, src, "blobs/big.bin", "application/octet-stream", big, nil)

	// Stat (HeadObject equivalent) — size, content type, metadata, sha256 ETag.
	attrs, err := src.Object("docs/hello.txt").Attrs(ctx)
	if err != nil {
		t.Fatalf("object Attrs: %v", err)
	}

	if attrs.Size != int64(len(textBody)) {
		t.Errorf("Attrs.Size = %d, want %d", attrs.Size, len(textBody))
	}

	if attrs.ContentType != "text/plain" {
		t.Errorf("Attrs.ContentType = %q, want text/plain", attrs.ContentType)
	}

	if attrs.Metadata["team"] != "e2e" || attrs.Metadata["run"] != "suite" {
		t.Errorf("Attrs.Metadata = %v, want team=e2e run=suite", attrs.Metadata)
	}

	// Survey: ETag is hex sha256 of the body (not MD5 like real GCS/S3).
	if attrs.Etag != sha256Hex(textBody) {
		t.Errorf("Attrs.Etag = %q, want sha256 %q", attrs.Etag, sha256Hex(textBody))
	}

	// Downloads round-trip byte-for-byte.
	if got := readObject(t, ctx, src, "docs/hello.txt"); !bytes.Equal(got, textBody) {
		t.Errorf("text body mismatch: got %q want %q", got, textBody)
	}

	if got := readObject(t, ctx, src, "empty.bin"); len(got) != 0 {
		t.Errorf("empty object came back with %d bytes", len(got))
	}

	if got := readObject(t, ctx, src, "blobs/big.bin"); !bytes.Equal(got, big) {
		t.Errorf("1MB body mismatch: got %d bytes (want %d), equal=false", len(got), len(big))
	}

	eattrs, err := src.Object("empty.bin").Attrs(ctx)
	if err != nil {
		t.Fatalf("empty Attrs: %v", err)
	}

	if eattrs.Size != 0 {
		t.Errorf("empty object Attrs.Size = %d, want 0", eattrs.Size)
	}

	// List everything — keys sorted lexically.
	keys, _ := listAll(t, ctx, src, nil)

	wantKeys := []string{"blobs/big.bin", "docs/hello.txt", "empty.bin"}
	if len(keys) != len(wantKeys) {
		t.Fatalf("list keys = %v, want %v", keys, wantKeys)
	}

	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Errorf("list keys[%d] = %q, want %q (sorted)", i, keys[i], wantKeys[i])
		}
	}

	// Cross-bucket copy: preserves ETag, content type, and metadata.
	copied, err := dst.Object("copied/hello.txt").CopierFrom(src.Object("docs/hello.txt")).Run(ctx)
	if err != nil {
		t.Fatalf("Copier.Run: %v", err)
	}

	if copied.Etag != attrs.Etag {
		t.Errorf("copy ETag = %q, want preserved %q", copied.Etag, attrs.Etag)
	}

	if copied.ContentType != "text/plain" {
		t.Errorf("copy ContentType = %q, want text/plain", copied.ContentType)
	}

	if copied.Metadata["team"] != "e2e" {
		t.Errorf("copy Metadata = %v, want team=e2e preserved", copied.Metadata)
	}

	if got := readObject(t, ctx, dst, "copied/hello.txt"); !bytes.Equal(got, textBody) {
		t.Errorf("copied body mismatch: got %q want %q", got, textBody)
	}

	// Delete all objects, then buckets.
	for _, k := range wantKeys {
		if err := src.Object(k).Delete(ctx); err != nil {
			t.Fatalf("delete object %q: %v", k, err)
		}
	}

	if err := dst.Object("copied/hello.txt").Delete(ctx); err != nil {
		t.Fatalf("delete copied object: %v", err)
	}

	if err := src.Delete(ctx); err != nil {
		t.Errorf("delete src bucket: %v", err)
	}

	if err := dst.Delete(ctx); err != nil {
		t.Errorf("delete dst bucket: %v", err)
	}

	// Bucket is gone: stat yields a typed not-found.
	if _, err := src.Attrs(ctx); !isSDKNotFound(err) {
		t.Errorf("Attrs on deleted bucket: got %v, want ErrBucketNotExist/404", err)
	}
}

// TestStorageListPrefixDelimiter verifies prefix filtering and
// delimiter roll-up into common prefixes, as the SDK exposes them (entries
// with only Prefix set).
func TestStorageListPrefixDelimiter(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-listing")

	for _, k := range []string{
		"logs/2024/a.log",
		"logs/2024/b.log",
		"logs/2025/c.log",
		"data/d.bin",
		"root.txt",
	} {
		putObject(t, ctx, bkt, k, "text/plain", []byte("x "+k), nil)
	}

	// Prefix only.
	keys, prefixes := listAll(t, ctx, bkt, &storage.Query{Prefix: "logs/2024/"})
	if len(prefixes) != 0 {
		t.Errorf("prefix-only list returned prefixes %v", prefixes)
	}

	want := []string{"logs/2024/a.log", "logs/2024/b.log"}
	if len(keys) != 2 || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("prefix list = %v, want %v", keys, want)
	}

	// Prefix + delimiter: subdirectories roll up into CommonPrefixes.
	keys, prefixes = listAll(t, ctx, bkt, &storage.Query{Prefix: "logs/", Delimiter: "/"})
	if len(keys) != 0 {
		t.Errorf("delimiter list leaked object keys %v", keys)
	}

	wantPrefixes := []string{"logs/2024/", "logs/2025/"}
	if len(prefixes) != 2 || prefixes[0] != wantPrefixes[0] || prefixes[1] != wantPrefixes[1] {
		t.Errorf("common prefixes = %v, want %v", prefixes, wantPrefixes)
	}

	// Root-level delimiter listing: top-level keys + top-level prefixes.
	keys, prefixes = listAll(t, ctx, bkt, &storage.Query{Delimiter: "/"})
	if len(keys) != 1 || keys[0] != "root.txt" {
		t.Errorf("root delimiter keys = %v, want [root.txt]", keys)
	}

	wantPrefixes = []string{"data/", "logs/"}
	if len(prefixes) != 2 || prefixes[0] != wantPrefixes[0] || prefixes[1] != wantPrefixes[1] {
		t.Errorf("root common prefixes = %v, want %v", prefixes, wantPrefixes)
	}

	// Prefix that matches nothing.
	keys, prefixes = listAll(t, ctx, bkt, &storage.Query{Prefix: "nope/"})
	if len(keys) != 0 || len(prefixes) != 0 {
		t.Errorf("no-match prefix returned keys=%v prefixes=%v", keys, prefixes)
	}
}

// TestStoragePagination pushes the iterator through multiple
// pages (maxResults + pageToken + nextPageToken on the wire) via
// iterator.NewPager.
func TestStoragePagination(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-paging")

	const total = 5

	var wantKeys []string

	for i := 0; i < total; i++ {
		k := fmt.Sprintf("page-%02d.txt", i)
		wantKeys = append(wantKeys, k)
		putObject(t, ctx, bkt, k, "text/plain", []byte(k), nil)
	}

	pager := iterator.NewPager(bkt.Objects(ctx, nil), 2, "")

	var (
		gotKeys   []string
		pageSizes []int
		pageCount int
	)

	for {
		var page []*storage.ObjectAttrs

		token, err := pager.NextPage(&page)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		pageCount++
		pageSizes = append(pageSizes, len(page))

		for _, a := range page {
			gotKeys = append(gotKeys, a.Name)
		}

		if token == "" {
			break
		}

		if pageCount > total {
			t.Fatalf("pagination did not terminate; got %d pages", pageCount)
		}
	}

	if pageCount != 3 {
		t.Errorf("page count = %d (sizes %v), want 3 pages of 2/2/1", pageCount, pageSizes)
	}

	if len(gotKeys) != total {
		t.Fatalf("paged keys = %v, want %d unique keys", gotKeys, total)
	}

	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Errorf("paged keys[%d] = %q, want %q (sorted, no dup/skip across pages)", i, gotKeys[i], wantKeys[i])
		}
	}
}

// TestStorageNotFoundErrors asserts the SDK-visible typed errors
// for reads/stats/deletes of nonexistent objects and buckets.
func TestStorageNotFoundErrors(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-errors")

	// Get (download) missing object.
	if _, err := bkt.Object("missing.txt").NewReader(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("NewReader missing object: got %v, want storage.ErrObjectNotExist", err)
	}

	// Stat missing object.
	if _, err := bkt.Object("missing.txt").Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("Attrs missing object: got %v, want storage.ErrObjectNotExist", err)
	}

	// Delete missing object.
	if err := bkt.Object("missing.txt").Delete(ctx); !isSDKNotFound(err) {
		t.Errorf("Delete missing object: got %v, want ErrObjectNotExist/404", err)
	}

	// Operations against a bucket that was never created.
	ghost := client.Bucket("e2e-no-such-bucket")

	if _, err := ghost.Attrs(ctx); !errors.Is(err, storage.ErrBucketNotExist) {
		t.Errorf("Attrs missing bucket: got %v, want storage.ErrBucketNotExist", err)
	}

	if _, err := ghost.Object("k").NewReader(ctx); !isSDKNotFound(err) {
		t.Errorf("NewReader in missing bucket: got %v, want not-found", err)
	}

	keysIt := ghost.Objects(ctx, nil)
	if _, err := keysIt.Next(); !isSDKNotFound(err) {
		t.Errorf("List in missing bucket: got %v, want not-found", err)
	}

	// Copy from a missing source object.
	if _, err := bkt.Object("dst").CopierFrom(bkt.Object("missing-src")).Run(ctx); !isSDKNotFound(err) {
		t.Errorf("Copy from missing source: got %v, want not-found", err)
	}
}

// TestStorageBucketEdgeCases covers duplicate creation, empty
// bucket names, deleting non-empty buckets, deleting missing buckets, and
// listing an empty bucket.
func TestStorageBucketEdgeCases(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-edge")

	// Duplicate create -> 409 conflict, surfaced as *googleapi.Error.
	err := client.Bucket("e2e-edge").Create(ctx, e2eProject, nil)

	var gerr *googleapi.Error

	if !errors.As(err, &gerr) || gerr.Code != http.StatusConflict {
		t.Errorf("duplicate Create: got %v, want googleapi.Error 409", err)
	}

	// Empty bucket name -> 400 invalid argument.
	err = client.Bucket("").Create(ctx, e2eProject, nil)
	if !errors.As(err, &gerr) || gerr.Code != http.StatusBadRequest {
		t.Errorf("empty-name Create: got %v, want googleapi.Error 400", err)
	}

	// List objects in a fresh (empty) bucket: iterator finishes immediately.
	it := bkt.Objects(ctx, nil)
	if _, err := it.Next(); !errors.Is(err, iterator.Done) {
		t.Errorf("empty-bucket list: got %v, want iterator.Done", err)
	}

	// Deleting a non-empty bucket must fail and leave contents intact.
	putObject(t, ctx, bkt, "keep.txt", "text/plain", []byte("keep"), nil)

	err = bkt.Delete(ctx)
	if err == nil {
		t.Fatalf("Delete of non-empty bucket succeeded, want error")
	}

	// Real GCS returns 409 conflict here; the emulator maps the driver's
	// FailedPrecondition through its default branch. Record what the SDK sees.
	if errors.As(err, &gerr) {
		t.Logf("non-empty bucket delete surfaced as googleapi.Error code=%d (real GCS: 409)", gerr.Code)
	} else {
		t.Logf("non-empty bucket delete surfaced as %T: %v", err, err)
	}

	if got := readObject(t, ctx, bkt, "keep.txt"); !bytes.Equal(got, []byte("keep")) {
		t.Errorf("object damaged by failed bucket delete: %q", got)
	}

	// Drain and delete for real.
	if err := bkt.Object("keep.txt").Delete(ctx); err != nil {
		t.Fatalf("delete keep.txt: %v", err)
	}

	if err := bkt.Delete(ctx); err != nil {
		t.Errorf("delete emptied bucket: %v", err)
	}

	// Deleting a bucket that does not exist -> typed not-found.
	if err := client.Bucket("e2e-never-existed").Delete(ctx); !isSDKNotFound(err) {
		t.Errorf("Delete missing bucket: got %v, want ErrBucketNotExist/404", err)
	}
}

// TestStorageKeysSlashesUnicode round-trips object keys
// containing slashes, spaces, and non-ASCII characters through upload,
// stat, download, list, and delete.
func TestStorageKeysSlashesUnicode(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-keys")

	keys := []string{
		"a/b/c/deep.txt",
		"with space/and space.txt",
		"über/schlüssel-✓.md",
		"日本語/ファイル.txt",
	}

	for _, k := range keys {
		putObject(t, ctx, bkt, k, "text/plain", []byte("body:"+k), nil)
	}

	for _, k := range keys {
		attrs, err := bkt.Object(k).Attrs(ctx)
		if err != nil {
			t.Errorf("Attrs %q: %v", k, err)
			continue
		}

		if attrs.Name != k {
			t.Errorf("Attrs.Name = %q, want %q", attrs.Name, k)
		}

		if got := readObject(t, ctx, bkt, k); !bytes.Equal(got, []byte("body:"+k)) {
			t.Errorf("body mismatch for %q: got %q", k, got)
		}
	}

	listed, _ := listAll(t, ctx, bkt, nil)

	wantSorted := append([]string(nil), keys...)
	sort.Strings(wantSorted)

	if len(listed) != len(wantSorted) {
		t.Fatalf("list = %v, want %v", listed, wantSorted)
	}

	for i := range wantSorted {
		if listed[i] != wantSorted[i] {
			t.Errorf("list[%d] = %q, want %q", i, listed[i], wantSorted[i])
		}
	}

	for _, k := range keys {
		if err := bkt.Object(k).Delete(ctx); err != nil {
			t.Errorf("delete %q: %v", k, err)
		}
	}
}

// TestStorageOverwriteSemantics verifies that re-uploading a key
// fully replaces content, content type, and ETag (survey: overwrite creates
// a fresh object).
func TestStorageOverwriteSemantics(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-overwrite")

	v1 := []byte("version one")
	v2 := []byte(`{"version": 2, "longer": true}`)

	putObject(t, ctx, bkt, "obj", "text/plain", v1, map[string]string{"gen": "1"})

	first, err := bkt.Object("obj").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs v1: %v", err)
	}

	putObject(t, ctx, bkt, "obj", "application/json", v2, nil)

	second, err := bkt.Object("obj").Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs v2: %v", err)
	}

	if second.ContentType != "application/json" {
		t.Errorf("overwritten ContentType = %q, want application/json", second.ContentType)
	}

	if second.Size != int64(len(v2)) {
		t.Errorf("overwritten Size = %d, want %d", second.Size, len(v2))
	}

	if second.Etag == first.Etag {
		t.Errorf("overwrite did not change ETag (%q)", second.Etag)
	}

	if second.Etag != sha256Hex(v2) {
		t.Errorf("overwritten Etag = %q, want sha256 %q", second.Etag, sha256Hex(v2))
	}

	if got := readObject(t, ctx, bkt, "obj"); !bytes.Equal(got, v2) {
		t.Errorf("overwritten body = %q, want %q", got, v2)
	}
}

// TestStorageTrailingBoundaryBytes uploads payloads whose final
// bytes are '-', '\n', or '\r\n' through the SDK's standard Writer
// (uploadType=multipart). The handler's parseMultipart trims trailing
// "\r\n-" characters off the payload to strip the boundary framing, which
// eats legitimate trailing bytes of the object itself.
func TestStorageTrailingBoundaryBytes(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-trailing")

	cases := map[string][]byte{
		"ends-with-dash":    []byte("checksum-suffix-"),
		"ends-with-newline": []byte("line1\nline2\n"),
		"ends-with-crlf":    []byte("windows data\r\n"),
	}

	for key, body := range cases {
		putObject(t, ctx, bkt, key, "application/octet-stream", body, nil)

		attrs, err := bkt.Object(key).Attrs(ctx)
		if err != nil {
			t.Errorf("Attrs %q: %v", key, err)
			continue
		}

		if attrs.Size != int64(len(body)) {
			t.Errorf("%s: stored Size = %d, want %d (trailing bytes eaten by multipart parsing)", key, attrs.Size, len(body))
		}

		if got := readObject(t, ctx, bkt, key); !bytes.Equal(got, body) {
			t.Errorf("%s: body = %q, want %q", key, got, body)
		}
	}
}

// TestStorageVersioningSurface documents the provider-specific
// surface: versioning is a driver-level boolean only. The bucket resource
// reports it disabled, and the JSON API exposes no PATCH endpoint, so the
// SDK cannot enable it over HTTP.
func TestStorageVersioningSurface(t *testing.T) {
	ctx, client := newStorageClient(t)
	bkt := mustCreateBucket(t, ctx, client, "e2e-versioning")

	attrs, err := bkt.Attrs(ctx)
	if err != nil {
		t.Fatalf("bucket Attrs: %v", err)
	}

	if attrs.VersioningEnabled {
		t.Errorf("fresh bucket reports VersioningEnabled=true, want false (survey: default false)")
	}

	// The GCS handler serves only GET/DELETE on /b/{bucket}; bucket Update
	// (PATCH) is not part of the HTTP surface — versioning is driver-only.
	_, err = bkt.Update(ctx, storage.BucketAttrsToUpdate{VersioningEnabled: true})
	if err == nil {
		t.Fatalf("bucket Update(VersioningEnabled) unexpectedly succeeded; HTTP surface was believed to be GET/DELETE only")
	}

	t.Logf("bucket Update over HTTP rejected as expected: %v", err)
}
