package s3

// e2e_suite_storage_test.go — suite cell STORAGE/aws/portable.
//
// Real-user-journey  tests exercising the portable driver.Bucket API of the
// AWS S3 mock directly (no HTTP layer). Covers full object lifecycle, typed
// error edge cases, pagination continuation tokens, multipart assembly order,
// copy semantics, versioning flag, lifecycle evaluation, presigned URLs and
// tagging/config surfaces.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// e2eEnv bundles a mock with its fake clock so journeys can advance time.
type e2eEnv struct {
	mock  *Mock
	clock *config.FakeClock
}

func newEnv() *e2eEnv {
	fc := config.NewFakeClock(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return &e2eEnv{mock: New(opts), clock: fc}
}

func e2eRequireNoErr(t *testing.T, err error, op string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: unexpected error: %v", op, err)
	}
}

func e2eRequireCode(t *testing.T, err error, want cerrors.Code, op string) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: expected error with code %v, got nil", op, want)
	}

	if got := cerrors.GetCode(err); got != want {
		t.Fatalf("%s: expected code %v, got %v (err=%v)", op, want, got, err)
	}
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// TestFullObjectLifecycle walks a complete user journey:
// bucket create -> put varied objects -> get -> head -> list (prefix,
// delimiter, pagination) -> copy -> delete objects -> delete bucket.
func TestFullObjectLifecycle(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "journey-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	// Bucket shows up in ListBuckets with region + creation time.
	buckets, err := m.ListBuckets(ctx)
	e2eRequireNoErr(t, err, "ListBuckets")

	if len(buckets) != 1 || buckets[0].Name != bucket {
		t.Fatalf("ListBuckets = %+v, want single bucket %q", buckets, bucket)
	}

	if buckets[0].Region != "us-east-1" {
		t.Fatalf("bucket region = %q, want us-east-1", buckets[0].Region)
	}

	// Varied payloads: empty body, small text, JSON with metadata, ~1MB binary.
	big := bytes.Repeat([]byte{0xAB, 0xCD, 0xEF, 0x01}, 256*1024) // 1 MiB

	objects := []struct {
		key         string
		data        []byte
		contentType string
		metadata    map[string]string
	}{
		{"docs/empty.txt", []byte{}, "text/plain", nil},
		{"docs/readme.txt", []byte("hello cloudemu"), "text/plain; charset=utf-8", nil},
		{"data/config.json", []byte(`{"env":"test"}`), "application/json", map[string]string{"owner": "e2e", "team": "storage"}},
		{"blobs/big.bin", big, "application/octet-stream", nil},
	}

	for _, o := range objects {
		e2eRequireNoErr(t, m.PutObject(ctx, bucket, o.key, o.data, o.contentType, o.metadata), "PutObject "+o.key)
	}

	// GetObject returns exact bytes, content type and sha256 ETag.
	for _, o := range objects {
		got, gerr := m.GetObject(ctx, bucket, o.key)
		e2eRequireNoErr(t, gerr, "GetObject "+o.key)

		if !bytes.Equal(got.Data, o.data) {
			t.Fatalf("GetObject %s: data mismatch (len got=%d want=%d)", o.key, len(got.Data), len(o.data))
		}

		if got.Info.ContentType != o.contentType {
			t.Fatalf("GetObject %s: contentType = %q, want %q", o.key, got.Info.ContentType, o.contentType)
		}

		if got.Info.ETag != sha256Hex(o.data) {
			t.Fatalf("GetObject %s: ETag = %q, want sha256 %q", o.key, got.Info.ETag, sha256Hex(o.data))
		}

		if got.Info.Size != int64(len(o.data)) {
			t.Fatalf("GetObject %s: size = %d, want %d", o.key, got.Info.Size, len(o.data))
		}
	}

	// HeadObject: metadata only, sizes match, metadata round-trips.
	head, err := m.HeadObject(ctx, bucket, "data/config.json")
	e2eRequireNoErr(t, err, "HeadObject")

	if head.Size != int64(len(`{"env":"test"}`)) {
		t.Fatalf("HeadObject size = %d", head.Size)
	}

	if head.Metadata["owner"] != "e2e" || head.Metadata["team"] != "storage" {
		t.Fatalf("HeadObject metadata = %v", head.Metadata)
	}

	// List everything: sorted lexically by key.
	all, err := m.ListObjects(ctx, bucket, driver.ListOptions{})
	e2eRequireNoErr(t, err, "ListObjects all")

	wantOrder := []string{"blobs/big.bin", "data/config.json", "docs/empty.txt", "docs/readme.txt"}
	if len(all.Objects) != len(wantOrder) {
		t.Fatalf("ListObjects returned %d objects, want %d", len(all.Objects), len(wantOrder))
	}

	for i, w := range wantOrder {
		if all.Objects[i].Key != w {
			t.Fatalf("ListObjects[%d] = %q, want %q (sorted)", i, all.Objects[i].Key, w)
		}
	}

	if all.IsTruncated {
		t.Fatal("ListObjects: unexpected truncation under default MaxKeys")
	}

	// Prefix filter.
	docs, err := m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "docs/"})
	e2eRequireNoErr(t, err, "ListObjects prefix")

	if len(docs.Objects) != 2 {
		t.Fatalf("prefix docs/: got %d objects, want 2", len(docs.Objects))
	}

	// Delimiter rolls up common prefixes; no direct-level objects here.
	rolled, err := m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/"})
	e2eRequireNoErr(t, err, "ListObjects delimiter")

	if len(rolled.Objects) != 0 {
		t.Fatalf("delimiter list: got %d objects at root, want 0", len(rolled.Objects))
	}

	wantPrefixes := []string{"blobs/", "data/", "docs/"}
	if len(rolled.CommonPrefixes) != len(wantPrefixes) {
		t.Fatalf("CommonPrefixes = %v, want %v", rolled.CommonPrefixes, wantPrefixes)
	}

	for i, p := range wantPrefixes {
		if rolled.CommonPrefixes[i] != p {
			t.Fatalf("CommonPrefixes[%d] = %q, want %q (sorted)", i, rolled.CommonPrefixes[i], p)
		}
	}

	// Copy cross-bucket: preserves ETag/metadata/content-type, fresh LastModified.
	const dstBucket = "journey-copy-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, dstBucket), "CreateBucket dst")

	env.clock.Advance(1 * time.Hour)

	e2eRequireNoErr(t, m.CopyObject(ctx, dstBucket, "copied/config.json", driver.CopySource{Bucket: bucket, Key: "data/config.json"}), "CopyObject")

	copied, err := m.GetObject(ctx, dstBucket, "copied/config.json")
	e2eRequireNoErr(t, err, "GetObject copied")

	src, err := m.GetObject(ctx, bucket, "data/config.json")
	e2eRequireNoErr(t, err, "GetObject source")

	if copied.Info.ETag != src.Info.ETag {
		t.Fatalf("copy ETag = %q, want source ETag %q", copied.Info.ETag, src.Info.ETag)
	}

	if copied.Info.ContentType != src.Info.ContentType {
		t.Fatalf("copy contentType = %q, want %q", copied.Info.ContentType, src.Info.ContentType)
	}

	if copied.Info.Metadata["owner"] != "e2e" {
		t.Fatalf("copy metadata = %v, want owner=e2e", copied.Info.Metadata)
	}

	if copied.Info.LastModified == src.Info.LastModified {
		t.Fatalf("copy LastModified %q should differ from source %q (clock advanced)", copied.Info.LastModified, src.Info.LastModified)
	}

	// Overwrite an existing key: data, content type and ETag all replaced.
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "docs/readme.txt", []byte("v2"), "text/markdown", nil), "PutObject overwrite")

	over, err := m.GetObject(ctx, bucket, "docs/readme.txt")
	e2eRequireNoErr(t, err, "GetObject overwritten")

	if string(over.Data) != "v2" || over.Info.ContentType != "text/markdown" || over.Info.ETag != sha256Hex([]byte("v2")) {
		t.Fatalf("overwrite not fully applied: %+v data=%q", over.Info, over.Data)
	}

	// Tear down: delete all objects, then buckets.
	for _, o := range objects {
		e2eRequireNoErr(t, m.DeleteObject(ctx, bucket, o.key), "DeleteObject "+o.key)
	}

	e2eRequireNoErr(t, m.DeleteObject(ctx, dstBucket, "copied/config.json"), "DeleteObject copied")
	e2eRequireNoErr(t, m.DeleteBucket(ctx, bucket), "DeleteBucket")
	e2eRequireNoErr(t, m.DeleteBucket(ctx, dstBucket), "DeleteBucket dst")

	buckets, err = m.ListBuckets(ctx)
	e2eRequireNoErr(t, err, "ListBuckets final")

	if len(buckets) != 0 {
		t.Fatalf("expected no buckets after teardown, got %v", buckets)
	}
}

// TestEdgeCases asserts the typed cerrors codes for the standard
// misuse paths plus exotic key names (slashes, unicode).
func TestEdgeCases(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "edge-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	// Empty bucket name -> InvalidArgument.
	e2eRequireCode(t, m.CreateBucket(ctx, ""), cerrors.InvalidArgument, "CreateBucket empty name")

	// Duplicate bucket -> AlreadyExists.
	e2eRequireCode(t, m.CreateBucket(ctx, bucket), cerrors.AlreadyExists, "CreateBucket duplicate")

	// Missing object in existing bucket -> NotFound (get, head, delete).
	_, err := m.GetObject(ctx, bucket, "no-such-key")
	e2eRequireCode(t, err, cerrors.NotFound, "GetObject missing key")

	_, err = m.HeadObject(ctx, bucket, "no-such-key")
	e2eRequireCode(t, err, cerrors.NotFound, "HeadObject missing key")

	e2eRequireCode(t, m.DeleteObject(ctx, bucket, "no-such-key"), cerrors.NotFound, "DeleteObject missing key")

	// Operations against a nonexistent bucket -> NotFound.
	_, err = m.GetObject(ctx, "ghost-bucket", "k")
	e2eRequireCode(t, err, cerrors.NotFound, "GetObject missing bucket")

	e2eRequireCode(t, m.PutObject(ctx, "ghost-bucket", "k", []byte("x"), "text/plain", nil), cerrors.NotFound, "PutObject missing bucket")

	_, err = m.ListObjects(ctx, "ghost-bucket", driver.ListOptions{})
	e2eRequireCode(t, err, cerrors.NotFound, "ListObjects missing bucket")

	e2eRequireCode(t, m.DeleteBucket(ctx, "ghost-bucket"), cerrors.NotFound, "DeleteBucket missing")

	// Copy error triage: src bucket vs src key vs dst bucket, all NotFound.
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "real-key", []byte("data"), "text/plain", nil), "PutObject real-key")

	e2eRequireCode(t, m.CopyObject(ctx, bucket, "d", driver.CopySource{Bucket: "ghost", Key: "real-key"}), cerrors.NotFound, "Copy missing src bucket")
	e2eRequireCode(t, m.CopyObject(ctx, bucket, "d", driver.CopySource{Bucket: bucket, Key: "ghost-key"}), cerrors.NotFound, "Copy missing src key")
	e2eRequireCode(t, m.CopyObject(ctx, "ghost", "d", driver.CopySource{Bucket: bucket, Key: "real-key"}), cerrors.NotFound, "Copy missing dst bucket")

	// Deleting a non-empty bucket -> FailedPrecondition.
	e2eRequireCode(t, m.DeleteBucket(ctx, bucket), cerrors.FailedPrecondition, "DeleteBucket non-empty")

	// List on a brand-new empty bucket: zero objects, no truncation, no token.
	e2eRequireNoErr(t, m.CreateBucket(ctx, "empty-bucket"), "CreateBucket empty-bucket")

	empty, err := m.ListObjects(ctx, "empty-bucket", driver.ListOptions{})
	e2eRequireNoErr(t, err, "ListObjects empty bucket")

	if len(empty.Objects) != 0 || empty.IsTruncated || empty.NextPageToken != "" {
		t.Fatalf("empty bucket list = %+v, want no objects/token/truncation", empty)
	}

	// Keys with slashes, deep nesting, unicode and spaces round-trip verbatim.
	weirdKeys := []string{
		"a/b/c/d/e/deep.txt",
		"unicode/日本語/ファイル.txt",
		"emoji/🚀-launch.bin",
		"spaces/key with spaces.txt",
		"dots/..hidden",
		"tilde/~user/file",
	}

	for _, k := range weirdKeys {
		body := []byte("payload:" + k)

		e2eRequireNoErr(t, m.PutObject(ctx, bucket, k, body, "application/octet-stream", nil), "PutObject "+k)

		got, gerr := m.GetObject(ctx, bucket, k)
		e2eRequireNoErr(t, gerr, "GetObject "+k)

		if got.Info.Key != k || !bytes.Equal(got.Data, body) {
			t.Fatalf("weird key %q: got key=%q data=%q", k, got.Info.Key, got.Data)
		}
	}

	// Unicode prefix listing works.
	uni, err := m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "unicode/"})
	e2eRequireNoErr(t, err, "ListObjects unicode prefix")

	if len(uni.Objects) != 1 || uni.Objects[0].Key != "unicode/日本語/ファイル.txt" {
		t.Fatalf("unicode prefix list = %+v", uni.Objects)
	}
}

// TestPaginationTokens drives a multi-page listing via opaque
// continuation tokens the way an SDK paginator would.
func TestPaginationTokens(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "page-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	const total = 25

	for i := 0; i < total; i++ {
		key := fmt.Sprintf("item-%03d", i)
		e2eRequireNoErr(t, m.PutObject(ctx, bucket, key, []byte(key), "text/plain", nil), "PutObject "+key)
	}

	var collected []string

	token := ""
	pages := 0

	for {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{MaxKeys: 10, PageToken: token})
		e2eRequireNoErr(t, err, "ListObjects page")

		pages++

		for _, o := range res.Objects {
			collected = append(collected, o.Key)
		}

		if !res.IsTruncated {
			if res.NextPageToken != "" {
				t.Fatalf("final page has NextPageToken %q, want empty", res.NextPageToken)
			}

			break
		}

		if res.NextPageToken == "" {
			t.Fatal("truncated page returned empty NextPageToken")
		}

		if len(res.Objects) != 10 {
			t.Fatalf("truncated page has %d objects, want 10", len(res.Objects))
		}

		token = res.NextPageToken
	}

	if pages != 3 {
		t.Fatalf("paginated in %d pages, want 3 (10+10+5)", pages)
	}

	if len(collected) != total {
		t.Fatalf("collected %d keys, want %d", len(collected), total)
	}

	for i, k := range collected {
		want := fmt.Sprintf("item-%03d", i)
		if k != want {
			t.Fatalf("collected[%d] = %q, want %q (global sort across pages)", i, k, want)
		}
	}

	// Survey behavior: CommonPrefixes are NOT paginated — full set on every page
	// even when object slice is truncated.
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("dir%d/file", i)
		e2eRequireNoErr(t, m.PutObject(ctx, bucket, key, []byte("x"), "text/plain", nil), "PutObject "+key)
	}

	first, err := m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/", MaxKeys: 2})
	e2eRequireNoErr(t, err, "ListObjects delimiter page1")

	if len(first.CommonPrefixes) != 5 {
		t.Fatalf("first page CommonPrefixes = %d, want all 5 (prefixes are never paginated)", len(first.CommonPrefixes))
	}
}

// TestMultipartJourney covers create -> out-of-order part upload ->
// complete (with parts listed out of order) -> assembled object, plus abort,
// list-uploads, and the InvalidArgument for completing with a missing part.
func TestMultipartJourney(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "mp-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	up, err := m.CreateMultipartUpload(ctx, bucket, "assembled/object.bin", "application/octet-stream")
	e2eRequireNoErr(t, err, "CreateMultipartUpload")

	if !strings.HasPrefix(up.UploadID, "upload-") {
		t.Fatalf("UploadID = %q, want upload- prefix", up.UploadID)
	}

	// Upload parts out of numeric order.
	partBodies := map[int][]byte{
		2: []byte("BBBB"),
		1: []byte("AAAA"),
		3: []byte("CCCC"),
	}

	var parts []driver.UploadPart

	for _, n := range []int{2, 1, 3} {
		p, perr := m.UploadPart(ctx, bucket, "assembled/object.bin", up.UploadID, n, partBodies[n])
		e2eRequireNoErr(t, perr, fmt.Sprintf("UploadPart %d", n))

		if p.ETag != sha256Hex(partBodies[n]) {
			t.Fatalf("part %d ETag = %q, want sha256 of body", n, p.ETag)
		}

		parts = append(parts, *p)
	}

	// In-progress upload is visible.
	ups, err := m.ListMultipartUploads(ctx, bucket)
	e2eRequireNoErr(t, err, "ListMultipartUploads")

	if len(ups) != 1 || ups[0].UploadID != up.UploadID || ups[0].Key != "assembled/object.bin" {
		t.Fatalf("ListMultipartUploads = %+v", ups)
	}

	// Complete listing parts in the (out-of-order) upload order; the S3 mock
	// must sort by PartNumber ascending before assembly.
	e2eRequireNoErr(t, m.CompleteMultipartUpload(ctx, bucket, "assembled/object.bin", up.UploadID, parts), "CompleteMultipartUpload")

	obj, err := m.GetObject(ctx, bucket, "assembled/object.bin")
	e2eRequireNoErr(t, err, "GetObject assembled")

	if string(obj.Data) != "AAAABBBBCCCC" {
		t.Fatalf("assembled data = %q, want AAAABBBBCCCC (ascending part order)", obj.Data)
	}

	if obj.Info.ContentType != "application/octet-stream" {
		t.Fatalf("assembled contentType = %q", obj.Info.ContentType)
	}

	if obj.Info.ETag != sha256Hex([]byte("AAAABBBBCCCC")) {
		t.Fatalf("assembled ETag = %q, want fresh sha256 over whole body", obj.Info.ETag)
	}

	if obj.Info.Metadata == nil {
		t.Fatal("assembled object Metadata is nil, want empty non-nil map")
	}

	// Upload is gone after completion.
	ups, err = m.ListMultipartUploads(ctx, bucket)
	e2eRequireNoErr(t, err, "ListMultipartUploads after complete")

	if len(ups) != 0 {
		t.Fatalf("uploads after complete = %+v, want none", ups)
	}

	// Completing again with same ID -> NotFound.
	e2eRequireCode(t, m.CompleteMultipartUpload(ctx, bucket, "k", up.UploadID, nil), cerrors.NotFound, "Complete on consumed upload")

	// Complete referencing an un-uploaded part number -> InvalidArgument.
	up2, err := m.CreateMultipartUpload(ctx, bucket, "second.bin", "application/octet-stream")
	e2eRequireNoErr(t, err, "CreateMultipartUpload 2")

	_, err = m.UploadPart(ctx, bucket, "second.bin", up2.UploadID, 1, []byte("X"))
	e2eRequireNoErr(t, err, "UploadPart 2")

	e2eRequireCode(t,
		m.CompleteMultipartUpload(ctx, bucket, "second.bin", up2.UploadID, []driver.UploadPart{{PartNumber: 1}, {PartNumber: 9}}),
		cerrors.InvalidArgument, "Complete with missing part")

	// Abort discards the upload; second abort -> NotFound.
	e2eRequireNoErr(t, m.AbortMultipartUpload(ctx, bucket, "second.bin", up2.UploadID), "AbortMultipartUpload")
	e2eRequireCode(t, m.AbortMultipartUpload(ctx, bucket, "second.bin", up2.UploadID), cerrors.NotFound, "Abort twice")

	// UploadPart against unknown upload id -> NotFound.
	_, err = m.UploadPart(ctx, bucket, "k", "upload-does-not-exist", 1, []byte("x"))
	e2eRequireCode(t, err, cerrors.NotFound, "UploadPart unknown id")
}

// TestVersioningFlag exercises the boolean-only versioning
// behavior: default false, toggle round-trip, NotFound on missing bucket.
func TestVersioningFlag(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "ver-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	v, err := m.GetBucketVersioning(ctx, bucket)
	e2eRequireNoErr(t, err, "GetBucketVersioning default")

	if v {
		t.Fatal("fresh bucket versioning = true, want false")
	}

	e2eRequireNoErr(t, m.SetBucketVersioning(ctx, bucket, true), "SetBucketVersioning on")

	v, err = m.GetBucketVersioning(ctx, bucket)
	e2eRequireNoErr(t, err, "GetBucketVersioning enabled")

	if !v {
		t.Fatal("versioning = false after enable")
	}

	// Mock limitation: no version history — overwriting under versioning still
	// replaces the object in place.
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "k", []byte("v1"), "text/plain", nil), "Put v1")
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "k", []byte("v2"), "text/plain", nil), "Put v2")

	got, err := m.GetObject(ctx, bucket, "k")
	e2eRequireNoErr(t, err, "Get after overwrite")

	if string(got.Data) != "v2" {
		t.Fatalf("data = %q, want v2 (flag-only versioning)", got.Data)
	}

	e2eRequireNoErr(t, m.SetBucketVersioning(ctx, bucket, false), "SetBucketVersioning off")

	v, err = m.GetBucketVersioning(ctx, bucket)
	e2eRequireNoErr(t, err, "GetBucketVersioning disabled")

	if v {
		t.Fatal("versioning = true after disable")
	}

	_, err = m.GetBucketVersioning(ctx, "ghost")
	e2eRequireCode(t, err, cerrors.NotFound, "GetBucketVersioning missing bucket")
}

// TestPresignedURLs checks method restriction, the 7-day expiry
// cap and the amazonaws.com URL shape with X-Amz-Expires.
func TestPresignedURLs(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "presign-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	pu, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: bucket, Key: "file.txt", Method: "GET", ExpiresIn: 30 * time.Minute,
	})
	e2eRequireNoErr(t, err, "Presign GET")

	if !strings.Contains(pu.URL, bucket+".s3.us-east-1.amazonaws.com/file.txt") {
		t.Fatalf("presigned URL = %q, want virtual-hosted amazonaws.com shape", pu.URL)
	}

	if !strings.Contains(pu.URL, "X-Amz-Expires=1800") {
		t.Fatalf("presigned URL = %q, want X-Amz-Expires=1800", pu.URL)
	}

	wantExpiry := env.clock.Now().UTC().Add(30 * time.Minute)
	if !pu.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", pu.ExpiresAt, wantExpiry)
	}

	// PUT allowed.
	_, err = m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{Bucket: bucket, Key: "k", Method: "PUT"})
	e2eRequireNoErr(t, err, "Presign PUT")

	// Other methods rejected.
	_, err = m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{Bucket: bucket, Key: "k", Method: "DELETE"})
	e2eRequireCode(t, err, cerrors.InvalidArgument, "Presign DELETE")

	// Expiry over 7 days rejected (S3-specific cap).
	_, err = m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{Bucket: bucket, Key: "k", Method: "GET", ExpiresIn: 8 * 24 * time.Hour})
	e2eRequireCode(t, err, cerrors.InvalidArgument, "Presign over 7d")

	// Missing bucket rejected.
	_, err = m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{Bucket: "ghost", Key: "k", Method: "GET"})
	e2eRequireCode(t, err, cerrors.NotFound, "Presign missing bucket")
}

// TestLifecycleEvaluation stores rules, ages objects with the fake
// clock and asserts EvaluateLifecycle reports (but never deletes) expired keys.
func TestLifecycleEvaluation(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "lc-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")

	// Never configured -> NotFound.
	_, err := m.GetLifecycleConfig(ctx, bucket)
	e2eRequireCode(t, err, cerrors.NotFound, "GetLifecycleConfig unset")

	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "tmp/old.log", []byte("old"), "text/plain", nil), "Put tmp/old.log")
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "keep/data.txt", []byte("keep"), "text/plain", nil), "Put keep/data.txt")

	cfg := driver.LifecycleConfig{Rules: []driver.LifecycleRule{
		{ID: "expire-tmp", Enabled: true, Prefix: "tmp/", ExpirationDays: 7},
		{ID: "disabled-rule", Enabled: false, Prefix: "keep/", ExpirationDays: 1},
	}}

	e2eRequireNoErr(t, m.PutLifecycleConfig(ctx, bucket, cfg), "PutLifecycleConfig")

	got, err := m.GetLifecycleConfig(ctx, bucket)
	e2eRequireNoErr(t, err, "GetLifecycleConfig")

	if len(got.Rules) != 2 || got.Rules[0].ID != "expire-tmp" {
		t.Fatalf("lifecycle config round-trip = %+v", got)
	}

	// Not old enough yet.
	expired, err := m.EvaluateLifecycle(ctx, bucket)
	e2eRequireNoErr(t, err, "EvaluateLifecycle fresh")

	if len(expired) != 0 {
		t.Fatalf("expired = %v, want none before aging", expired)
	}

	// Age past 7 days: tmp/ rule fires; disabled keep/ rule must not.
	env.clock.Advance(8 * 24 * time.Hour)

	// A new object written now is not expired.
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "tmp/new.log", []byte("new"), "text/plain", nil), "Put tmp/new.log")

	expired, err = m.EvaluateLifecycle(ctx, bucket)
	e2eRequireNoErr(t, err, "EvaluateLifecycle aged")

	if len(expired) != 1 || expired[0] != "tmp/old.log" {
		t.Fatalf("expired = %v, want [tmp/old.log] only", expired)
	}

	// Evaluation reports only — objects still exist.
	if _, err = m.GetObject(ctx, bucket, "tmp/old.log"); err != nil {
		t.Fatalf("expired object was deleted by evaluation: %v", err)
	}
}

// TestTaggingAndBucketConfigs walks tagging (object + bucket) and
// the policy/CORS/encryption config surfaces including their unset-NotFound
// and clear semantics.
func TestTaggingAndBucketConfigs(t *testing.T) {
	env := newEnv()
	m := env.mock
	ctx := context.Background()

	const bucket = "cfg-bucket"

	e2eRequireNoErr(t, m.CreateBucket(ctx, bucket), "CreateBucket")
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "tagged.txt", []byte("x"), "text/plain", nil), "PutObject")

	// Object tagging: empty non-nil map when unset.
	tags, err := m.GetObjectTagging(ctx, bucket, "tagged.txt")
	e2eRequireNoErr(t, err, "GetObjectTagging unset")

	if tags == nil || len(tags) != 0 {
		t.Fatalf("unset object tags = %v, want empty non-nil map", tags)
	}

	e2eRequireNoErr(t, m.PutObjectTagging(ctx, bucket, "tagged.txt", map[string]string{"env": "prod", "tier": "gold"}), "PutObjectTagging")

	// Put replaces the whole set.
	e2eRequireNoErr(t, m.PutObjectTagging(ctx, bucket, "tagged.txt", map[string]string{"env": "staging"}), "PutObjectTagging replace")

	tags, err = m.GetObjectTagging(ctx, bucket, "tagged.txt")
	e2eRequireNoErr(t, err, "GetObjectTagging")

	if len(tags) != 1 || tags["env"] != "staging" {
		t.Fatalf("object tags = %v, want full replacement {env:staging}", tags)
	}

	// Overwriting the object via PutObject resets tags (fresh object).
	e2eRequireNoErr(t, m.PutObject(ctx, bucket, "tagged.txt", []byte("y"), "text/plain", nil), "PutObject overwrite")

	tags, err = m.GetObjectTagging(ctx, bucket, "tagged.txt")
	e2eRequireNoErr(t, err, "GetObjectTagging after overwrite")

	if len(tags) != 0 {
		t.Fatalf("tags after overwrite = %v, want cleared", tags)
	}

	// Tag ops on a missing object -> NotFound.
	e2eRequireCode(t, m.PutObjectTagging(ctx, bucket, "ghost", map[string]string{"a": "b"}), cerrors.NotFound, "PutObjectTagging missing")
	e2eRequireCode(t, m.DeleteObjectTagging(ctx, bucket, "ghost"), cerrors.NotFound, "DeleteObjectTagging missing")

	// Bucket tagging.
	btags, err := m.GetBucketTagging(ctx, bucket)
	e2eRequireNoErr(t, err, "GetBucketTagging unset")

	if btags == nil || len(btags) != 0 {
		t.Fatalf("unset bucket tags = %v, want empty non-nil map", btags)
	}

	e2eRequireNoErr(t, m.PutBucketTagging(ctx, bucket, map[string]string{"cost-center": "42"}), "PutBucketTagging")

	btags, err = m.GetBucketTagging(ctx, bucket)
	e2eRequireNoErr(t, err, "GetBucketTagging")

	if btags["cost-center"] != "42" {
		t.Fatalf("bucket tags = %v", btags)
	}

	e2eRequireNoErr(t, m.DeleteBucketTagging(ctx, bucket), "DeleteBucketTagging")
	e2eRequireNoErr(t, m.DeleteBucketTagging(ctx, bucket), "DeleteBucketTagging idempotent")

	// Policy: NotFound when unset, round-trip, delete idempotent.
	_, err = m.GetBucketPolicy(ctx, bucket)
	e2eRequireCode(t, err, cerrors.NotFound, "GetBucketPolicy unset")

	policy := driver.BucketPolicy{Version: "2012-10-17", Statements: []driver.PolicyStatement{{
		Effect: "Allow", Principal: "*", Actions: []string{"s3:GetObject"},
		Resources: []string{"arn:aws:s3:::" + bucket + "/*"},
	}}}

	e2eRequireNoErr(t, m.PutBucketPolicy(ctx, bucket, policy), "PutBucketPolicy")

	gotPolicy, err := m.GetBucketPolicy(ctx, bucket)
	e2eRequireNoErr(t, err, "GetBucketPolicy")

	if gotPolicy.Version != "2012-10-17" || len(gotPolicy.Statements) != 1 || gotPolicy.Statements[0].Effect != "Allow" {
		t.Fatalf("policy round-trip = %+v", gotPolicy)
	}

	e2eRequireNoErr(t, m.DeleteBucketPolicy(ctx, bucket), "DeleteBucketPolicy")
	e2eRequireNoErr(t, m.DeleteBucketPolicy(ctx, bucket), "DeleteBucketPolicy idempotent")

	_, err = m.GetBucketPolicy(ctx, bucket)
	e2eRequireCode(t, err, cerrors.NotFound, "GetBucketPolicy after delete")

	// CORS: NotFound when unset, round-trip, delete always succeeds.
	_, err = m.GetCORSConfig(ctx, bucket)
	e2eRequireCode(t, err, cerrors.NotFound, "GetCORSConfig unset")

	cors := driver.CORSConfig{Rules: []driver.CORSRule{{
		AllowedOrigins: []string{"https://example.com"},
		AllowedMethods: []string{"GET", "PUT"},
		MaxAgeSeconds:  3600,
	}}}

	e2eRequireNoErr(t, m.PutCORSConfig(ctx, bucket, cors), "PutCORSConfig")

	gotCors, err := m.GetCORSConfig(ctx, bucket)
	e2eRequireNoErr(t, err, "GetCORSConfig")

	if len(gotCors.Rules) != 1 || gotCors.Rules[0].AllowedOrigins[0] != "https://example.com" {
		t.Fatalf("CORS round-trip = %+v", gotCors)
	}

	e2eRequireNoErr(t, m.DeleteCORSConfig(ctx, bucket), "DeleteCORSConfig")

	_, err = m.GetCORSConfig(ctx, bucket)
	e2eRequireCode(t, err, cerrors.NotFound, "GetCORSConfig after delete")

	// Encryption: NotFound when unset, round-trip.
	_, err = m.GetEncryptionConfig(ctx, bucket)
	e2eRequireCode(t, err, cerrors.NotFound, "GetEncryptionConfig unset")

	e2eRequireNoErr(t, m.PutEncryptionConfig(ctx, bucket, driver.EncryptionConfig{Enabled: true, Algorithm: "aws:kms", KeyID: "key-123"}), "PutEncryptionConfig")

	enc, err := m.GetEncryptionConfig(ctx, bucket)
	e2eRequireNoErr(t, err, "GetEncryptionConfig")

	if !enc.Enabled || enc.Algorithm != "aws:kms" || enc.KeyID != "key-123" {
		t.Fatalf("encryption round-trip = %+v", enc)
	}
}
