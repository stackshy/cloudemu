// Package gcs e2e suite tests: STORAGE / gcp / portable.
//
// These tests exercise the portable driver.Bucket API of the GCS mock as a
// real user would: full bucket/object lifecycles, listing with prefixes,
// delimiters and pagination, copies, multipart uploads, and the typed error
// surface for edge cases.
package gcs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// newMock returns a GCS mock with a controllable fake clock.
func newMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(clk),
		config.WithRegion("us-central1"),
		config.WithProjectID("e2e-suite"),
	)

	return New(opts), clk
}

func requireCode(t *testing.T, err error, want cerrors.Code) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, want, cerrors.GetCode(err), "unexpected error code for %v", err)
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// TestFullObjectLifecycle walks a complete user journey:
// create bucket -> put objects of varied shapes -> get -> head -> list ->
// copy -> delete objects -> delete bucket.
func TestFullObjectLifecycle(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock(t)

	const bucket = "e2e-journey"

	require.NoError(t, m.CreateBucket(ctx, bucket))

	buckets, err := m.ListBuckets(ctx)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, bucket, buckets[0].Name)
	assert.Equal(t, "us-central1", buckets[0].Region)
	assert.NotEmpty(t, buckets[0].CreatedAt)

	bigPayload := bytes.Repeat([]byte("cloudemu!"), 1<<17) // ~1.18 MB

	objects := []struct {
		key         string
		data        []byte
		contentType string
		metadata    map[string]string
	}{
		{"docs/readme.txt", []byte("hello gcs"), "text/plain", map[string]string{"owner": "e2e"}},
		{"docs/api/spec.json", []byte(`{"v":1}`), "application/json", nil},
		{"empty.bin", []byte{}, "application/octet-stream", nil},
		{"media/big.blob", bigPayload, "application/octet-stream", map[string]string{"size-class": "large"}},
		{"unicode/übersicht-日本語 ✓.txt", []byte("unicode key body"), "text/plain; charset=utf-8", nil},
	}

	for _, o := range objects {
		require.NoError(t, m.PutObject(ctx, bucket, o.key, o.data, o.contentType, o.metadata))
	}

	// Get: full data copy, sha256 ETag, verbatim content type + metadata.
	for _, o := range objects {
		got, getErr := m.GetObject(ctx, bucket, o.key)
		require.NoError(t, getErr, "GetObject(%q)", o.key)
		assert.Equal(t, o.data, got.Data, "data mismatch for %q", o.key)
		assert.Equal(t, o.contentType, got.Info.ContentType)
		assert.Equal(t, sha256Hex(o.data), got.Info.ETag)
		assert.Equal(t, int64(len(o.data)), got.Info.Size)

		for k, v := range o.metadata {
			assert.Equal(t, v, got.Info.Metadata[k])
		}
	}

	// Returned data must be a defensive copy: mutation must not leak back.
	got, err := m.GetObject(ctx, bucket, "docs/readme.txt")
	require.NoError(t, err)

	got.Data[0] = 'X'

	again, err := m.GetObject(ctx, bucket, "docs/readme.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello gcs"), again.Data, "GetObject must return a copy of stored data")

	// Head: metadata only, matches Get info.
	info, err := m.HeadObject(ctx, bucket, "media/big.blob")
	require.NoError(t, err)
	assert.Equal(t, int64(len(bigPayload)), info.Size)
	assert.Equal(t, sha256Hex(bigPayload), info.ETag)
	assert.Equal(t, "large", info.Metadata["size-class"])

	// Overwrite replaces everything, including tags and ETag.
	require.NoError(t, m.PutObjectTagging(ctx, bucket, "docs/readme.txt", map[string]string{"env": "test"}))
	clk.Advance(2 * time.Second)
	require.NoError(t, m.PutObject(ctx, bucket, "docs/readme.txt", []byte("v2 body"), "text/markdown", nil))

	got, err = m.GetObject(ctx, bucket, "docs/readme.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2 body"), got.Data)
	assert.Equal(t, "text/markdown", got.Info.ContentType)
	assert.Equal(t, sha256Hex([]byte("v2 body")), got.Info.ETag)

	tags, err := m.GetObjectTagging(ctx, bucket, "docs/readme.txt")
	require.NoError(t, err)
	assert.Empty(t, tags, "overwrite must reset object tags")

	// List with prefix.
	res, err := m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "docs/"})
	require.NoError(t, err)
	require.Len(t, res.Objects, 2)
	assert.Equal(t, "docs/api/spec.json", res.Objects[0].Key)
	assert.Equal(t, "docs/readme.txt", res.Objects[1].Key)
	assert.False(t, res.IsTruncated)

	// List with prefix + delimiter: rolls nested keys into CommonPrefixes.
	res, err = m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "docs/", Delimiter: "/"})
	require.NoError(t, err)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/readme.txt", res.Objects[0].Key)
	assert.Equal(t, []string{"docs/api/"}, res.CommonPrefixes)

	// Delimiter with no prefix: only top-level keys plus one prefix per "dir".
	res, err = m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/"})
	require.NoError(t, err)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "empty.bin", res.Objects[0].Key)
	assert.Equal(t, []string{"docs/", "media/", "unicode/"}, res.CommonPrefixes)

	// Copy within the bucket and cross-bucket: ETag/metadata/content-type
	// preserved, LastModified refreshed from the clock.
	const dstBucket = "e2e-journey-copy"
	require.NoError(t, m.CreateBucket(ctx, dstBucket))

	srcInfo, err := m.HeadObject(ctx, bucket, "media/big.blob")
	require.NoError(t, err)

	clk.Advance(5 * time.Second)
	require.NoError(t, m.CopyObject(ctx, dstBucket, "backup/big.blob",
		driver.CopySource{Bucket: bucket, Key: "media/big.blob"}))

	cp, err := m.GetObject(ctx, dstBucket, "backup/big.blob")
	require.NoError(t, err)
	assert.Equal(t, bigPayload, cp.Data)
	assert.Equal(t, srcInfo.ETag, cp.Info.ETag, "copy must preserve ETag")
	assert.Equal(t, srcInfo.ContentType, cp.Info.ContentType)
	assert.Equal(t, "large", cp.Info.Metadata["size-class"])
	assert.NotEqual(t, srcInfo.LastModified, cp.Info.LastModified,
		"copy must stamp a fresh LastModified")

	// Delete all objects, then the buckets.
	for _, o := range objects {
		require.NoError(t, m.DeleteObject(ctx, bucket, o.key))
	}

	res, err = m.ListObjects(ctx, bucket, driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, res.Objects)

	require.NoError(t, m.DeleteBucket(ctx, bucket))
	require.NoError(t, m.DeleteObject(ctx, dstBucket, "backup/big.blob"))
	require.NoError(t, m.DeleteBucket(ctx, dstBucket))

	buckets, err = m.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Empty(t, buckets)
}

// TestEdgeCases asserts the typed error surface for the usual
// user mistakes.
func TestEdgeCases(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-edge"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	t.Run("create bucket with empty name", func(t *testing.T) {
		requireCode(t, m.CreateBucket(ctx, ""), cerrors.InvalidArgument)
	})

	t.Run("duplicate bucket create", func(t *testing.T) {
		requireCode(t, m.CreateBucket(ctx, bucket), cerrors.AlreadyExists)
	})

	t.Run("get nonexistent object", func(t *testing.T) {
		_, err := m.GetObject(ctx, bucket, "ghost.txt")
		requireCode(t, err, cerrors.NotFound)
	})

	t.Run("head nonexistent object", func(t *testing.T) {
		_, err := m.HeadObject(ctx, bucket, "ghost.txt")
		requireCode(t, err, cerrors.NotFound)
	})

	t.Run("delete nonexistent object", func(t *testing.T) {
		requireCode(t, m.DeleteObject(ctx, bucket, "ghost.txt"), cerrors.NotFound)
	})

	t.Run("operations on nonexistent bucket", func(t *testing.T) {
		_, err := m.GetObject(ctx, "no-such-bucket", "k")
		requireCode(t, err, cerrors.NotFound)

		requireCode(t, m.PutObject(ctx, "no-such-bucket", "k", []byte("x"), "", nil), cerrors.NotFound)

		_, err = m.ListObjects(ctx, "no-such-bucket", driver.ListOptions{})
		requireCode(t, err, cerrors.NotFound)

		requireCode(t, m.DeleteBucket(ctx, "no-such-bucket"), cerrors.NotFound)
	})

	t.Run("delete non-empty bucket", func(t *testing.T) {
		require.NoError(t, m.PutObject(ctx, bucket, "keeper", []byte("x"), "text/plain", nil))
		requireCode(t, m.DeleteBucket(ctx, bucket), cerrors.FailedPrecondition)

		// Cleanup restores deletability.
		require.NoError(t, m.DeleteObject(ctx, bucket, "keeper"))
		require.NoError(t, m.DeleteBucket(ctx, bucket))
	})

	t.Run("list on empty bucket", func(t *testing.T) {
		require.NoError(t, m.CreateBucket(ctx, "e2e-empty"))

		res, err := m.ListObjects(ctx, "e2e-empty", driver.ListOptions{Prefix: "any/", Delimiter: "/"})
		require.NoError(t, err)
		assert.Empty(t, res.Objects)
		assert.Empty(t, res.CommonPrefixes)
		assert.False(t, res.IsTruncated)
		assert.Empty(t, res.NextPageToken)
	})

	t.Run("copy source and destination errors", func(t *testing.T) {
		require.NoError(t, m.CreateBucket(ctx, "e2e-copy-err"))
		require.NoError(t, m.PutObject(ctx, "e2e-copy-err", "src", []byte("x"), "", nil))

		// Missing source bucket.
		err := m.CopyObject(ctx, "e2e-copy-err", "dst", driver.CopySource{Bucket: "nope", Key: "src"})
		requireCode(t, err, cerrors.NotFound)

		// Missing source key.
		err = m.CopyObject(ctx, "e2e-copy-err", "dst", driver.CopySource{Bucket: "e2e-copy-err", Key: "nope"})
		requireCode(t, err, cerrors.NotFound)

		// Missing destination bucket.
		err = m.CopyObject(ctx, "nope", "dst", driver.CopySource{Bucket: "e2e-copy-err", Key: "src"})
		requireCode(t, err, cerrors.NotFound)
	})
}

// TestKeyNamesSlashesUnicode stores keys with slashes, unicode
// and awkward characters, and gets them all back byte-identical.
func TestKeyNamesSlashesUnicode(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-keys"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	keys := []string{
		"plain",
		"a/b/c/deeply/nested/key.txt",
		"trailing-slash/",
		"//double-leading-slash",
		"日本語/ファイル.txt",
		"emoji-🚀-key",
		"space in key.txt",
		"specials!@#$%^&*()[]{}",
	}

	for i, k := range keys {
		body := []byte(fmt.Sprintf("body-%d", i))
		require.NoError(t, m.PutObject(ctx, bucket, k, body, "text/plain", nil), "put %q", k)
	}

	for i, k := range keys {
		got, err := m.GetObject(ctx, bucket, k)
		require.NoError(t, err, "get %q", k)
		assert.Equal(t, []byte(fmt.Sprintf("body-%d", i)), got.Data)
		assert.Equal(t, k, got.Info.Key)
	}

	// All keys visible in a full list, lexically sorted.
	res, err := m.ListObjects(ctx, bucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, res.Objects, len(keys))

	for i := 1; i < len(res.Objects); i++ {
		assert.True(t, res.Objects[i-1].Key < res.Objects[i].Key,
			"keys must be sorted: %q >= %q", res.Objects[i-1].Key, res.Objects[i].Key)
	}

	for _, k := range keys {
		require.NoError(t, m.DeleteObject(ctx, bucket, k))
	}
}

// TestPaginationTokens pages through a large listing with opaque
// continuation tokens and checks CommonPrefixes are NOT paginated.
func TestPaginationTokens(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-pages"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	const total = 25

	want := make([]string, 0, total)
	for i := 0; i < total; i++ {
		k := fmt.Sprintf("obj-%03d", i)
		want = append(want, k)
		require.NoError(t, m.PutObject(ctx, bucket, k, []byte(k), "text/plain", nil))
	}

	// Page through with MaxKeys=7.
	var collected []string

	token := ""
	pages := 0

	for {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{MaxKeys: 7, PageToken: token})
		require.NoError(t, err)

		pages++
		require.LessOrEqual(t, len(res.Objects), 7)

		for _, o := range res.Objects {
			collected = append(collected, o.Key)
		}

		if !res.IsTruncated {
			assert.Empty(t, res.NextPageToken, "final page must have no token")
			break
		}

		require.NotEmpty(t, res.NextPageToken, "truncated page must carry a token")
		token = res.NextPageToken
		require.Less(t, pages, 20, "runaway pagination")
	}

	assert.Equal(t, 4, pages, "25 keys / 7 per page = 4 pages")
	assert.Equal(t, want, collected, "pagination must cover every key exactly once, in order")

	// MaxKeys <= 0 falls back to the 1000 default: single page.
	res, err := m.ListObjects(ctx, bucket, driver.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, res.Objects, total)
	assert.False(t, res.IsTruncated)

	// CommonPrefixes are returned in full even when object pages truncate.
	for i := 0; i < 5; i++ {
		require.NoError(t, m.PutObject(ctx, bucket,
			fmt.Sprintf("dir%d/file", i), []byte("x"), "text/plain", nil))
	}

	res, err = m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/", MaxKeys: 3})
	require.NoError(t, err)
	assert.True(t, res.IsTruncated)
	assert.Len(t, res.Objects, 3)
	assert.Len(t, res.CommonPrefixes, 5, "common prefixes are never paginated")
}

// TestMultipartLifecycle drives create -> upload -> list ->
// complete and abort paths, including the GCS-mock-specific caller-order
// assembly.
func TestMultipartLifecycle(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-multipart"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	up, err := m.CreateMultipartUpload(ctx, bucket, "assembled.bin", "application/octet-stream")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(up.UploadID, "upload-"), "uploadID %q", up.UploadID)
	assert.Equal(t, bucket, up.Bucket)
	assert.Equal(t, "assembled.bin", up.Key)

	part1 := bytes.Repeat([]byte("A"), 128)
	part2 := bytes.Repeat([]byte("B"), 64)

	p1, err := m.UploadPart(ctx, bucket, "assembled.bin", up.UploadID, 1, part1)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(part1), p1.ETag)
	assert.Equal(t, int64(128), p1.Size)

	p2, err := m.UploadPart(ctx, bucket, "assembled.bin", up.UploadID, 2, part2)
	require.NoError(t, err)

	// In-progress upload is listed.
	ups, err := m.ListMultipartUploads(ctx, bucket)
	require.NoError(t, err)
	require.Len(t, ups, 1)
	assert.Equal(t, up.UploadID, ups[0].UploadID)

	// Complete referencing an un-uploaded part is InvalidArgument.
	err = m.CompleteMultipartUpload(ctx, bucket, "assembled.bin", up.UploadID,
		[]driver.UploadPart{{PartNumber: 99}})
	requireCode(t, err, cerrors.InvalidArgument)

	// GCS mock assembles in *caller list order*, not part-number order
	// (unlike the S3 mock). Listing [2, 1] yields part2+part1.
	require.NoError(t, m.CompleteMultipartUpload(ctx, bucket, "assembled.bin", up.UploadID,
		[]driver.UploadPart{*p2, *p1}))

	obj, err := m.GetObject(ctx, bucket, "assembled.bin")
	require.NoError(t, err)
	assert.Equal(t, append(append([]byte{}, part2...), part1...), obj.Data,
		"GCS mock concatenates parts in caller order")
	assert.Equal(t, "application/octet-stream", obj.Info.ContentType)
	assert.Equal(t, sha256Hex(obj.Data), obj.Info.ETag)
	assert.NotNil(t, obj.Info.Metadata, "completed multipart object has non-nil metadata")

	// Upload is consumed by Complete: further ops on it are NotFound.
	_, err = m.UploadPart(ctx, bucket, "assembled.bin", up.UploadID, 3, []byte("late"))
	requireCode(t, err, cerrors.NotFound)

	requireCode(t, m.AbortMultipartUpload(ctx, bucket, "assembled.bin", up.UploadID), cerrors.NotFound)

	// Abort path: a new upload disappears without creating an object.
	up2, err := m.CreateMultipartUpload(ctx, bucket, "never-finished.bin", "")
	require.NoError(t, err)

	_, err = m.UploadPart(ctx, bucket, "never-finished.bin", up2.UploadID, 1, []byte("gone"))
	require.NoError(t, err)
	require.NoError(t, m.AbortMultipartUpload(ctx, bucket, "never-finished.bin", up2.UploadID))

	ups, err = m.ListMultipartUploads(ctx, bucket)
	require.NoError(t, err)
	assert.Empty(t, ups)

	_, err = m.GetObject(ctx, bucket, "never-finished.bin")
	requireCode(t, err, cerrors.NotFound)

	// Parts not referenced in Complete are silently dropped.
	up3, err := m.CreateMultipartUpload(ctx, bucket, "partial.bin", "")
	require.NoError(t, err)

	q1, err := m.UploadPart(ctx, bucket, "partial.bin", up3.UploadID, 1, []byte("kept"))
	require.NoError(t, err)
	_, err = m.UploadPart(ctx, bucket, "partial.bin", up3.UploadID, 2, []byte("dropped"))
	require.NoError(t, err)

	require.NoError(t, m.CompleteMultipartUpload(ctx, bucket, "partial.bin", up3.UploadID,
		[]driver.UploadPart{*q1}))

	obj, err = m.GetObject(ctx, bucket, "partial.bin")
	require.NoError(t, err)
	assert.Equal(t, []byte("kept"), obj.Data)
}

// TestVersioningFlag checks the boolean-flag-only versioning
// surface (no version history is kept by the mock).
func TestVersioningFlag(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-versioning"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	on, err := m.GetBucketVersioning(ctx, bucket)
	require.NoError(t, err)
	assert.False(t, on, "fresh bucket defaults to versioning disabled")

	require.NoError(t, m.SetBucketVersioning(ctx, bucket, true))

	on, err = m.GetBucketVersioning(ctx, bucket)
	require.NoError(t, err)
	assert.True(t, on)

	// Flag only: overwriting with versioning on still replaces the object.
	require.NoError(t, m.PutObject(ctx, bucket, "k", []byte("v1"), "", nil))
	require.NoError(t, m.PutObject(ctx, bucket, "k", []byte("v2"), "", nil))

	got, err := m.GetObject(ctx, bucket, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), got.Data, "mock keeps no version history")

	require.NoError(t, m.SetBucketVersioning(ctx, bucket, false))

	on, err = m.GetBucketVersioning(ctx, bucket)
	require.NoError(t, err)
	assert.False(t, on)

	_, err = m.GetBucketVersioning(ctx, "no-such-bucket")
	requireCode(t, err, cerrors.NotFound)
}

// TestLifecycleEvaluation stores rules and evaluates expiry
// against the fake clock; evaluation reports but never deletes.
func TestLifecycleEvaluation(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock(t)

	const bucket = "e2e-lifecycle"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	// Unset config: Get is NotFound, Evaluate returns nothing.
	_, err := m.GetLifecycleConfig(ctx, bucket)
	requireCode(t, err, cerrors.NotFound)

	expired, err := m.EvaluateLifecycle(ctx, bucket)
	require.NoError(t, err)
	assert.Empty(t, expired)

	require.NoError(t, m.PutObject(ctx, bucket, "tmp/old.log", []byte("old"), "", nil))
	require.NoError(t, m.PutObject(ctx, bucket, "keep/forever.txt", []byte("keep"), "", nil))

	cfg := driver.LifecycleConfig{Rules: []driver.LifecycleRule{
		{ID: "expire-tmp", Enabled: true, Prefix: "tmp/", ExpirationDays: 7},
		{ID: "disabled-rule", Enabled: false, Prefix: "keep/", ExpirationDays: 1},
	}}
	require.NoError(t, m.PutLifecycleConfig(ctx, bucket, cfg))

	got, err := m.GetLifecycleConfig(ctx, bucket)
	require.NoError(t, err)
	require.Len(t, got.Rules, 2)
	assert.Equal(t, "expire-tmp", got.Rules[0].ID)

	// Not old enough yet.
	clk.Advance(6 * 24 * time.Hour)

	expired, err = m.EvaluateLifecycle(ctx, bucket)
	require.NoError(t, err)
	assert.Empty(t, expired)

	// Cross the 7-day threshold: tmp/old.log expires; disabled rule ignored.
	clk.Advance(25 * time.Hour)

	expired, err = m.EvaluateLifecycle(ctx, bucket)
	require.NoError(t, err)
	assert.Equal(t, []string{"tmp/old.log"}, expired)

	// Evaluation is report-only: object still there.
	_, err = m.GetObject(ctx, bucket, "tmp/old.log")
	require.NoError(t, err, "EvaluateLifecycle must not delete objects")
}

// TestPresignedURLs verifies method validation, expiry cap, and
// the GCS-shaped URL.
func TestPresignedURLs(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-presign"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	for _, method := range []string{"GET", "PUT"} {
		u, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: bucket, Key: "file.txt", Method: method, ExpiresIn: time.Hour,
		})
		require.NoError(t, err, "method %s", method)
		assert.Equal(t, method, u.Method)
		assert.Contains(t, u.URL, "storage.googleapis.com/"+bucket+"/file.txt")
		assert.Contains(t, u.URL, "X-Goog-Signature=")
		assert.Contains(t, u.URL, "X-Goog-Expires=3600")
	}

	// Only GET/PUT allowed.
	for _, method := range []string{"POST", "DELETE", "get"} {
		_, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: bucket, Key: "file.txt", Method: method,
		})
		requireCode(t, err, cerrors.InvalidArgument)
	}

	// Expiry over 7 days rejected; zero falls back to a default.
	_, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: bucket, Key: "file.txt", Method: "GET", ExpiresIn: 8 * 24 * time.Hour,
	})
	requireCode(t, err, cerrors.InvalidArgument)

	u, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: bucket, Key: "file.txt", Method: "GET",
	})
	require.NoError(t, err)
	assert.Contains(t, u.URL, "X-Goog-Expires=900", "default expiry is 15 minutes")

	// Bucket must exist.
	_, err = m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: "no-such-bucket", Key: "k", Method: "GET",
	})
	requireCode(t, err, cerrors.NotFound)
}

// TestTaggingJourney covers object and bucket tagging semantics:
// empty non-nil map when unset, full replacement on Put, cleared on Delete.
func TestTaggingJourney(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-tagging"
	require.NoError(t, m.CreateBucket(ctx, bucket))
	require.NoError(t, m.PutObject(ctx, bucket, "obj", []byte("x"), "", nil))

	// Object tagging.
	tags, err := m.GetObjectTagging(ctx, bucket, "obj")
	require.NoError(t, err)
	require.NotNil(t, tags)
	assert.Empty(t, tags)

	require.NoError(t, m.PutObjectTagging(ctx, bucket, "obj", map[string]string{"a": "1", "b": "2"}))
	require.NoError(t, m.PutObjectTagging(ctx, bucket, "obj", map[string]string{"c": "3"}))

	tags, err = m.GetObjectTagging(ctx, bucket, "obj")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"c": "3"}, tags, "Put replaces the entire tag set")

	require.NoError(t, m.DeleteObjectTagging(ctx, bucket, "obj"))

	tags, err = m.GetObjectTagging(ctx, bucket, "obj")
	require.NoError(t, err)
	assert.Empty(t, tags)

	// Tagging a missing object fails typed.
	requireCode(t, m.PutObjectTagging(ctx, bucket, "ghost", map[string]string{"x": "y"}), cerrors.NotFound)
	_, err = m.GetObjectTagging(ctx, bucket, "ghost")
	requireCode(t, err, cerrors.NotFound)
	requireCode(t, m.DeleteObjectTagging(ctx, bucket, "ghost"), cerrors.NotFound)

	// Tags are NOT copied by CopyObject.
	require.NoError(t, m.PutObjectTagging(ctx, bucket, "obj", map[string]string{"copied": "no"}))
	require.NoError(t, m.CopyObject(ctx, bucket, "obj-copy", driver.CopySource{Bucket: bucket, Key: "obj"}))

	tags, err = m.GetObjectTagging(ctx, bucket, "obj-copy")
	require.NoError(t, err)
	assert.Empty(t, tags, "CopyObject must not carry tags to the destination")

	// Bucket tagging.
	btags, err := m.GetBucketTagging(ctx, bucket)
	require.NoError(t, err)
	require.NotNil(t, btags)
	assert.Empty(t, btags)

	require.NoError(t, m.PutBucketTagging(ctx, bucket, map[string]string{"team": "storage"}))

	btags, err = m.GetBucketTagging(ctx, bucket)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"team": "storage"}, btags)

	require.NoError(t, m.DeleteBucketTagging(ctx, bucket))

	btags, err = m.GetBucketTagging(ctx, bucket)
	require.NoError(t, err)
	assert.Empty(t, btags)
}

// TestBucketConfigSurfaces covers policy, CORS and encryption
// config round-trips including their NotFound-when-unset contract.
func TestBucketConfigSurfaces(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock(t)

	const bucket = "e2e-configs"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	t.Run("bucket policy", func(t *testing.T) {
		_, err := m.GetBucketPolicy(ctx, bucket)
		requireCode(t, err, cerrors.NotFound)

		policy := driver.BucketPolicy{
			Version: "2012-10-17",
			Statements: []driver.PolicyStatement{{
				Effect: "Allow", Principal: "*",
				Actions:   []string{"storage.objects.get"},
				Resources: []string{"projects/_/buckets/" + bucket + "/objects/*"},
			}},
		}
		require.NoError(t, m.PutBucketPolicy(ctx, bucket, policy))

		got, err := m.GetBucketPolicy(ctx, bucket)
		require.NoError(t, err)
		assert.Equal(t, policy.Version, got.Version)
		require.Len(t, got.Statements, 1)
		assert.Equal(t, "Allow", got.Statements[0].Effect)

		// Delete is idempotent.
		require.NoError(t, m.DeleteBucketPolicy(ctx, bucket))
		require.NoError(t, m.DeleteBucketPolicy(ctx, bucket))

		_, err = m.GetBucketPolicy(ctx, bucket)
		requireCode(t, err, cerrors.NotFound)
	})

	t.Run("CORS config", func(t *testing.T) {
		_, err := m.GetCORSConfig(ctx, bucket)
		requireCode(t, err, cerrors.NotFound)

		cfg := driver.CORSConfig{Rules: []driver.CORSRule{{
			AllowedOrigins: []string{"https://example.com"},
			AllowedMethods: []string{"GET", "PUT"},
			MaxAgeSeconds:  3600,
		}}}
		require.NoError(t, m.PutCORSConfig(ctx, bucket, cfg))

		got, err := m.GetCORSConfig(ctx, bucket)
		require.NoError(t, err)
		require.Len(t, got.Rules, 1)
		assert.Equal(t, []string{"https://example.com"}, got.Rules[0].AllowedOrigins)

		require.NoError(t, m.DeleteCORSConfig(ctx, bucket))
		require.NoError(t, m.DeleteCORSConfig(ctx, bucket))

		_, err = m.GetCORSConfig(ctx, bucket)
		requireCode(t, err, cerrors.NotFound)
	})

	t.Run("encryption config", func(t *testing.T) {
		_, err := m.GetEncryptionConfig(ctx, bucket)
		requireCode(t, err, cerrors.NotFound)

		cfg := driver.EncryptionConfig{Enabled: true, Algorithm: "AES256", KeyID: "projects/p/keys/k"}
		require.NoError(t, m.PutEncryptionConfig(ctx, bucket, cfg))

		got, err := m.GetEncryptionConfig(ctx, bucket)
		require.NoError(t, err)
		assert.True(t, got.Enabled)
		assert.Equal(t, "AES256", got.Algorithm)
		assert.Equal(t, "projects/p/keys/k", got.KeyID)
	})

	t.Run("configs on missing bucket", func(t *testing.T) {
		requireCode(t, m.PutBucketPolicy(ctx, "nope", driver.BucketPolicy{}), cerrors.NotFound)
		requireCode(t, m.PutCORSConfig(ctx, "nope", driver.CORSConfig{}), cerrors.NotFound)
		requireCode(t, m.PutEncryptionConfig(ctx, "nope", driver.EncryptionConfig{}), cerrors.NotFound)
		requireCode(t, m.PutLifecycleConfig(ctx, "nope", driver.LifecycleConfig{}), cerrors.NotFound)
	})
}
