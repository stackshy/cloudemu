// e2e_suite_storage_test.go — suite cell STORAGE/azure/portable.
//
// Real-user-journey  tests that exercise the Azure Blob Storage mock
// through the portable driver.Bucket API directly.
package blobstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// newMock builds a mock with a controllable fake clock so tests can
// advance time (LastModified, lifecycle evaluation).
func newMock() (*Mock, *config.FakeClock) {
	clk := config.NewFakeClock(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("eastus"))

	return New(opts), clk
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// TestFullLifecycle walks a complete user journey:
// create container -> put varied blobs -> get -> head -> list -> copy ->
// delete blobs -> delete container.
func TestFullLifecycle(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()

	const bucket = "journey-container"

	// --- create bucket, verify listing ---
	require.NoError(t, m.CreateBucket(ctx, bucket))

	buckets, err := m.ListBuckets(ctx)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, bucket, buckets[0].Name)
	assert.Equal(t, "eastus", buckets[0].Region)
	assert.NotEmpty(t, buckets[0].CreatedAt)

	// --- put varied objects ---
	large := bytes.Repeat([]byte("cloudemu-1MB-payload-"), 50000) // ~1.05 MB
	require.Greater(t, len(large), 1<<20)

	objects := []struct {
		key         string
		data        []byte
		contentType string
		metadata    map[string]string
	}{
		{"docs/readme.txt", []byte("hello azure"), "text/plain", map[string]string{"author": "e2e"}},
		{"docs/guide.json", []byte(`{"k":"v"}`), "application/json", nil},
		{"media/photo.bin", []byte{0x00, 0xFF, 0x10, 0x80}, "application/octet-stream", nil},
		{"empty.dat", []byte{}, "application/octet-stream", nil},
		{"big/blob.bin", large, "application/octet-stream", nil},
	}

	for _, o := range objects {
		require.NoError(t, m.PutObject(ctx, bucket, o.key, o.data, o.contentType, o.metadata))
	}

	// --- get each back, verify data / content-type / ETag ---
	for _, o := range objects {
		got, gerr := m.GetObject(ctx, bucket, o.key)
		require.NoError(t, gerr, "get %s", o.key)
		assert.Equal(t, o.data, got.Data, "data round-trip for %s", o.key)
		assert.Equal(t, o.contentType, got.Info.ContentType)
		assert.Equal(t, sha256Hex(o.data), got.Info.ETag, "ETag is hex sha256 of body")
		assert.Equal(t, int64(len(o.data)), got.Info.Size)
	}

	// empty object: ETag of empty body is the sha256 of no bytes
	emptyObj, err := m.GetObject(ctx, bucket, "empty.dat")
	require.NoError(t, err)
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		emptyObj.Info.ETag)
	assert.Empty(t, emptyObj.Data)

	// --- head (metadata only) matches get ---
	info, err := m.HeadObject(ctx, bucket, "docs/readme.txt")
	require.NoError(t, err)
	assert.Equal(t, "docs/readme.txt", info.Key)
	assert.Equal(t, int64(len("hello azure")), info.Size)
	assert.Equal(t, "text/plain", info.ContentType)
	assert.Equal(t, map[string]string{"author": "e2e"}, info.Metadata)

	// --- list all: sorted keys ---
	all, err := m.ListObjects(ctx, bucket, driver.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all.Objects, 5)
	assert.False(t, all.IsTruncated)

	var keys []string
	for _, o := range all.Objects {
		keys = append(keys, o.Key)
	}
	assert.Equal(t, []string{"big/blob.bin", "docs/guide.json", "docs/readme.txt", "empty.dat", "media/photo.bin"}, keys)

	// --- list with prefix ---
	docs, err := m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "docs/"})
	require.NoError(t, err)
	require.Len(t, docs.Objects, 2)
	assert.Equal(t, "docs/guide.json", docs.Objects[0].Key)
	assert.Equal(t, "docs/readme.txt", docs.Objects[1].Key)

	// --- list with delimiter rolls up common prefixes ---
	rolled, err := m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/"})
	require.NoError(t, err)
	assert.Equal(t, []string{"big/", "docs/", "media/"}, rolled.CommonPrefixes)
	require.Len(t, rolled.Objects, 1)
	assert.Equal(t, "empty.dat", rolled.Objects[0].Key)

	// --- copy within and across containers ---
	const bucket2 = "journey-container-2"
	require.NoError(t, m.CreateBucket(ctx, bucket2))

	clk.Advance(2 * time.Hour) // so LastModified visibly differs

	require.NoError(t, m.CopyObject(ctx, bucket2, "copied/readme.txt",
		driver.CopySource{Bucket: bucket, Key: "docs/readme.txt"}))

	src, err := m.GetObject(ctx, bucket, "docs/readme.txt")
	require.NoError(t, err)
	dst, err := m.GetObject(ctx, bucket2, "copied/readme.txt")
	require.NoError(t, err)

	assert.Equal(t, src.Data, dst.Data)
	assert.Equal(t, src.Info.ETag, dst.Info.ETag, "copy preserves ETag")
	assert.Equal(t, src.Info.ContentType, dst.Info.ContentType, "copy preserves content type")
	assert.Equal(t, src.Info.Metadata, dst.Info.Metadata, "copy preserves metadata")
	assert.NotEqual(t, src.Info.LastModified, dst.Info.LastModified, "copy gets fresh LastModified")

	// --- delete objects then containers ---
	for _, o := range objects {
		require.NoError(t, m.DeleteObject(ctx, bucket, o.key))
	}

	empty, err := m.ListObjects(ctx, bucket, driver.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, empty.Objects)

	require.NoError(t, m.DeleteBucket(ctx, bucket))
	require.NoError(t, m.DeleteObject(ctx, bucket2, "copied/readme.txt"))
	require.NoError(t, m.DeleteBucket(ctx, bucket2))

	buckets, err = m.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Empty(t, buckets)
}

// TestEdgeCases covers typed-error behavior for the common
// mistakes a real user makes.
func TestEdgeCases(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "edge-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	t.Run("get nonexistent object is NotFound", func(t *testing.T) {
		_, err := m.GetObject(ctx, bucket, "no-such-key")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err), "got %v", err)
		assert.Contains(t, err.Error(), "blob", "Azure wording uses blob")
	})

	t.Run("get from nonexistent bucket is NotFound", func(t *testing.T) {
		_, err := m.GetObject(ctx, "ghost-container", "key")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
		assert.Contains(t, err.Error(), "container", "Azure wording uses container")
	})

	t.Run("delete nonexistent object is NotFound", func(t *testing.T) {
		err := m.DeleteObject(ctx, bucket, "no-such-key")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("head nonexistent object is NotFound", func(t *testing.T) {
		_, err := m.HeadObject(ctx, bucket, "no-such-key")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("duplicate bucket create is AlreadyExists", func(t *testing.T) {
		err := m.CreateBucket(ctx, bucket)
		require.Error(t, err)
		assert.True(t, cerrors.IsAlreadyExists(err), "got %v", err)
	})

	t.Run("empty bucket name is InvalidArgument", func(t *testing.T) {
		err := m.CreateBucket(ctx, "")
		require.Error(t, err)
		assert.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
	})

	t.Run("delete non-empty bucket is FailedPrecondition", func(t *testing.T) {
		require.NoError(t, m.PutObject(ctx, bucket, "blocker", []byte("x"), "text/plain", nil))

		err := m.DeleteBucket(ctx, bucket)
		require.Error(t, err)
		assert.True(t, cerrors.IsFailedPrecondition(err), "got %v", err)

		require.NoError(t, m.DeleteObject(ctx, bucket, "blocker"))
	})

	t.Run("delete nonexistent bucket is NotFound", func(t *testing.T) {
		err := m.DeleteBucket(ctx, "ghost-container")
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("list on empty bucket succeeds with no objects", func(t *testing.T) {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{})
		require.NoError(t, err)
		assert.Empty(t, res.Objects)
		assert.Empty(t, res.CommonPrefixes)
		assert.False(t, res.IsTruncated)
		assert.Empty(t, res.NextPageToken)
	})

	t.Run("list on nonexistent bucket is NotFound", func(t *testing.T) {
		_, err := m.ListObjects(ctx, "ghost-container", driver.ListOptions{})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("put into nonexistent bucket is NotFound", func(t *testing.T) {
		err := m.PutObject(ctx, "ghost-container", "k", []byte("v"), "text/plain", nil)
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})
}

// TestSpecialKeyNames verifies slash-, unicode-, and
// space-containing keys survive the round trip.
func TestSpecialKeyNames(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "keys-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	specialKeys := []string{
		"a/b/c/deeply/nested/key.txt",
		"ünïcode/日本語/ключ.txt",
		"emoji-🚀-key",
		"key with spaces.txt",
		"trailing-slash/",
		"dots..in..key",
	}

	for i, k := range specialKeys {
		data := []byte(fmt.Sprintf("payload-%d", i))
		require.NoError(t, m.PutObject(ctx, bucket, k, data, "text/plain", nil), "put %q", k)

		got, err := m.GetObject(ctx, bucket, k)
		require.NoError(t, err, "get %q", k)
		assert.Equal(t, data, got.Data)
		assert.Equal(t, k, got.Info.Key)
	}

	// unicode prefix filtering works bytewise
	res, err := m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "ünïcode/"})
	require.NoError(t, err)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "ünïcode/日本語/ключ.txt", res.Objects[0].Key)

	// delete them all
	for _, k := range specialKeys {
		require.NoError(t, m.DeleteObject(ctx, bucket, k))
	}

	require.NoError(t, m.DeleteBucket(ctx, bucket))
}

// TestPagination walks pages via opaque continuation tokens.
func TestPagination(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "page-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	const total = 7
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("item-%02d", i)
		require.NoError(t, m.PutObject(ctx, bucket, key, []byte(key), "text/plain", nil))
	}

	var collected []string

	token := ""
	pages := 0

	for {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{MaxKeys: 3, PageToken: token})
		require.NoError(t, err)

		pages++
		for _, o := range res.Objects {
			collected = append(collected, o.Key)
		}

		if !res.IsTruncated {
			assert.Empty(t, res.NextPageToken, "final page has no token")
			break
		}

		require.NotEmpty(t, res.NextPageToken, "truncated page must carry a token")
		token = res.NextPageToken
		require.Less(t, pages, 10, "pagination did not terminate")
	}

	assert.Equal(t, 3, pages, "7 items at MaxKeys=3 is 3 pages")
	require.Len(t, collected, total)

	// keys arrive sorted with no dupes across pages
	for i := 0; i < total; i++ {
		assert.Equal(t, fmt.Sprintf("item-%02d", i), collected[i])
	}

	// MaxKeys<=0 falls back to the 1000 default: everything on one page
	res, err := m.ListObjects(ctx, bucket, driver.ListOptions{MaxKeys: 0})
	require.NoError(t, err)
	assert.Len(t, res.Objects, total)
	assert.False(t, res.IsTruncated)
}

// TestDelimiterRollup verifies survey behavior: common prefixes
// are rolled up but NOT paginated — the full prefix set is always returned.
func TestDelimiterRollup(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "delim-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	keys := []string{
		"logs/2026/01/a.log",
		"logs/2026/02/b.log",
		"logs/2027/01/c.log",
		"data/x.csv",
		"data/y.csv",
		"root.txt",
	}
	for _, k := range keys {
		require.NoError(t, m.PutObject(ctx, bucket, k, []byte("x"), "text/plain", nil))
	}

	t.Run("top level rollup", func(t *testing.T) {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/"})
		require.NoError(t, err)
		assert.Equal(t, []string{"data/", "logs/"}, res.CommonPrefixes)
		require.Len(t, res.Objects, 1)
		assert.Equal(t, "root.txt", res.Objects[0].Key)
	})

	t.Run("nested prefix plus delimiter", func(t *testing.T) {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{Prefix: "logs/", Delimiter: "/"})
		require.NoError(t, err)
		assert.Equal(t, []string{"logs/2026/", "logs/2027/"}, res.CommonPrefixes)
		assert.Empty(t, res.Objects)
	})

	t.Run("prefixes not paginated even with MaxKeys 1", func(t *testing.T) {
		res, err := m.ListObjects(ctx, bucket, driver.ListOptions{Delimiter: "/", MaxKeys: 1})
		require.NoError(t, err)
		// full prefix set regardless of page size (documented mock behavior)
		assert.Equal(t, []string{"data/", "logs/"}, res.CommonPrefixes)
	})
}

// TestOverwriteSemantics verifies overwriting a key replaces
// everything, including tags.
func TestOverwriteSemantics(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "overwrite-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	require.NoError(t, m.PutObject(ctx, bucket, "k", []byte("version-1"), "text/plain",
		map[string]string{"gen": "1"}))
	require.NoError(t, m.PutObjectTagging(ctx, bucket, "k", map[string]string{"env": "prod"}))

	// overwrite with new content type and metadata
	require.NoError(t, m.PutObject(ctx, bucket, "k", []byte("v2"), "application/json",
		map[string]string{"gen": "2"}))

	got, err := m.GetObject(ctx, bucket, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), got.Data)
	assert.Equal(t, "application/json", got.Info.ContentType)
	assert.Equal(t, sha256Hex([]byte("v2")), got.Info.ETag)
	assert.Equal(t, map[string]string{"gen": "2"}, got.Info.Metadata)

	tags, err := m.GetObjectTagging(ctx, bucket, "k")
	require.NoError(t, err)
	assert.Empty(t, tags, "overwrite creates a fresh object with no tags")
}

// TestCopyEdgeCases covers the three distinct NotFound copy
// failures and the tags-not-copied behavior.
func TestCopyEdgeCases(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	require.NoError(t, m.CreateBucket(ctx, "src"))
	require.NoError(t, m.CreateBucket(ctx, "dst"))
	require.NoError(t, m.PutObject(ctx, "src", "obj", []byte("data"), "text/plain", nil))
	require.NoError(t, m.PutObjectTagging(ctx, "src", "obj", map[string]string{"team": "core"}))

	t.Run("missing source bucket", func(t *testing.T) {
		err := m.CopyObject(ctx, "dst", "k", driver.CopySource{Bucket: "nope", Key: "obj"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
		assert.Contains(t, err.Error(), "source container")
	})

	t.Run("missing source key", func(t *testing.T) {
		err := m.CopyObject(ctx, "dst", "k", driver.CopySource{Bucket: "src", Key: "nope"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
		assert.Contains(t, err.Error(), "source blob")
	})

	t.Run("missing destination bucket", func(t *testing.T) {
		err := m.CopyObject(ctx, "nope", "k", driver.CopySource{Bucket: "src", Key: "obj"})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
		assert.Contains(t, err.Error(), "destination container")
	})

	t.Run("tags are not copied", func(t *testing.T) {
		require.NoError(t, m.CopyObject(ctx, "dst", "copy", driver.CopySource{Bucket: "src", Key: "obj"}))

		tags, err := m.GetObjectTagging(ctx, "dst", "copy")
		require.NoError(t, err)
		assert.Empty(t, tags)
	})
}

// TestMultipart drives the full multipart journey including the
// Azure-specific caller-order assembly behavior noted in the survey.
func TestMultipart(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "mp-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	t.Run("happy path in-order assembly", func(t *testing.T) {
		up, err := m.CreateMultipartUpload(ctx, bucket, "assembled.bin", "application/octet-stream")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(up.UploadID, "upload-"), "uploadID %q", up.UploadID)
		assert.Equal(t, "assembled.bin", up.Key)

		p1, err := m.UploadPart(ctx, bucket, "assembled.bin", up.UploadID, 1, []byte("part-one|"))
		require.NoError(t, err)
		assert.Equal(t, sha256Hex([]byte("part-one|")), p1.ETag)

		p2, err := m.UploadPart(ctx, bucket, "assembled.bin", up.UploadID, 2, []byte("part-two"))
		require.NoError(t, err)

		require.NoError(t, m.CompleteMultipartUpload(ctx, bucket, "assembled.bin", up.UploadID,
			[]driver.UploadPart{*p1, *p2}))

		obj, err := m.GetObject(ctx, bucket, "assembled.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("part-one|part-two"), obj.Data)
		assert.Equal(t, "application/octet-stream", obj.Info.ContentType)
		assert.Equal(t, sha256Hex([]byte("part-one|part-two")), obj.Info.ETag)
		assert.NotNil(t, obj.Info.Metadata, "completed multipart object has empty non-nil metadata")
		assert.Empty(t, obj.Info.Metadata)

		// upload handle is gone after completion
		uploads, err := m.ListMultipartUploads(ctx, bucket)
		require.NoError(t, err)
		assert.Empty(t, uploads)
	})

	t.Run("out-of-order complete concatenates in caller order (Azure mock asymmetry)", func(t *testing.T) {
		up, err := m.CreateMultipartUpload(ctx, bucket, "ooo.bin", "application/octet-stream")
		require.NoError(t, err)

		p1, err := m.UploadPart(ctx, bucket, "ooo.bin", up.UploadID, 1, []byte("AAA"))
		require.NoError(t, err)
		p2, err := m.UploadPart(ctx, bucket, "ooo.bin", up.UploadID, 2, []byte("BBB"))
		require.NoError(t, err)

		// list parts in reverse: the Azure mock does NOT sort by part number
		require.NoError(t, m.CompleteMultipartUpload(ctx, bucket, "ooo.bin", up.UploadID,
			[]driver.UploadPart{*p2, *p1}))

		obj, err := m.GetObject(ctx, bucket, "ooo.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("BBBAAA"), obj.Data,
			"documented mock asymmetry: azure assembles in caller-listed order, unlike the S3 mock")
	})

	t.Run("complete referencing un-uploaded part is InvalidArgument", func(t *testing.T) {
		up, err := m.CreateMultipartUpload(ctx, bucket, "bad.bin", "")
		require.NoError(t, err)

		_, err = m.UploadPart(ctx, bucket, "bad.bin", up.UploadID, 1, []byte("x"))
		require.NoError(t, err)

		err = m.CompleteMultipartUpload(ctx, bucket, "bad.bin", up.UploadID,
			[]driver.UploadPart{{PartNumber: 1}, {PartNumber: 99}})
		require.Error(t, err)
		assert.True(t, cerrors.IsInvalidArgument(err), "got %v", err)

		// cleanup so it does not linger in later assertions
		require.NoError(t, m.AbortMultipartUpload(ctx, bucket, "bad.bin", up.UploadID))
	})

	t.Run("unlisted parts are silently dropped", func(t *testing.T) {
		up, err := m.CreateMultipartUpload(ctx, bucket, "partial.bin", "")
		require.NoError(t, err)

		p1, err := m.UploadPart(ctx, bucket, "partial.bin", up.UploadID, 1, []byte("keep"))
		require.NoError(t, err)
		_, err = m.UploadPart(ctx, bucket, "partial.bin", up.UploadID, 2, []byte("drop"))
		require.NoError(t, err)

		require.NoError(t, m.CompleteMultipartUpload(ctx, bucket, "partial.bin", up.UploadID,
			[]driver.UploadPart{*p1}))

		obj, err := m.GetObject(ctx, bucket, "partial.bin")
		require.NoError(t, err)
		assert.Equal(t, []byte("keep"), obj.Data)
	})

	t.Run("abort discards upload; abort of unknown upload is NotFound", func(t *testing.T) {
		up, err := m.CreateMultipartUpload(ctx, bucket, "aborted.bin", "")
		require.NoError(t, err)

		_, err = m.UploadPart(ctx, bucket, "aborted.bin", up.UploadID, 1, []byte("x"))
		require.NoError(t, err)

		require.NoError(t, m.AbortMultipartUpload(ctx, bucket, "aborted.bin", up.UploadID))

		// object never materialized
		_, err = m.GetObject(ctx, bucket, "aborted.bin")
		assert.True(t, cerrors.IsNotFound(err))

		// second abort: NotFound
		err = m.AbortMultipartUpload(ctx, bucket, "aborted.bin", up.UploadID)
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("list in-progress uploads sorted by ID", func(t *testing.T) {
		upA, err := m.CreateMultipartUpload(ctx, bucket, "list-a.bin", "")
		require.NoError(t, err)
		upB, err := m.CreateMultipartUpload(ctx, bucket, "list-b.bin", "")
		require.NoError(t, err)

		uploads, err := m.ListMultipartUploads(ctx, bucket)
		require.NoError(t, err)
		require.Len(t, uploads, 2)

		assert.LessOrEqual(t, uploads[0].UploadID, uploads[1].UploadID, "sorted by upload ID")

		ids := []string{uploads[0].UploadID, uploads[1].UploadID}
		assert.Contains(t, ids, upA.UploadID)
		assert.Contains(t, ids, upB.UploadID)

		require.NoError(t, m.AbortMultipartUpload(ctx, bucket, "list-a.bin", upA.UploadID))
		require.NoError(t, m.AbortMultipartUpload(ctx, bucket, "list-b.bin", upB.UploadID))
	})

	t.Run("upload part to unknown upload is NotFound", func(t *testing.T) {
		_, err := m.UploadPart(ctx, bucket, "k", "upload-bogus", 1, []byte("x"))
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})
}

// TestVersioning verifies the boolean-flag-only versioning model.
func TestVersioning(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "ver-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	enabled, err := m.GetBucketVersioning(ctx, bucket)
	require.NoError(t, err)
	assert.False(t, enabled, "fresh container defaults to versioning disabled")

	require.NoError(t, m.SetBucketVersioning(ctx, bucket, true))

	enabled, err = m.GetBucketVersioning(ctx, bucket)
	require.NoError(t, err)
	assert.True(t, enabled)

	// flag only — overwriting an object keeps exactly one copy (no history)
	require.NoError(t, m.PutObject(ctx, bucket, "k", []byte("v1"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, bucket, "k", []byte("v2"), "text/plain", nil))

	got, err := m.GetObject(ctx, bucket, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v2"), got.Data)

	require.NoError(t, m.SetBucketVersioning(ctx, bucket, false))

	enabled, err = m.GetBucketVersioning(ctx, bucket)
	require.NoError(t, err)
	assert.False(t, enabled)

	_, err = m.GetBucketVersioning(ctx, "ghost")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

// TestLifecycleEvaluation drives lifecycle config plus fake-clock
// based expiry evaluation (report-only, no deletion).
func TestLifecycleEvaluation(t *testing.T) {
	ctx := context.Background()
	m, clk := newMock()

	const bucket = "lc-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	t.Run("get before configure is NotFound", func(t *testing.T) {
		_, err := m.GetLifecycleConfig(ctx, bucket)
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("evaluate before configure returns nothing", func(t *testing.T) {
		expired, err := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, err)
		assert.Empty(t, expired)
	})

	// objects written at T0
	require.NoError(t, m.PutObject(ctx, bucket, "tmp/old-1", []byte("x"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, bucket, "tmp/old-2", []byte("x"), "text/plain", nil))
	require.NoError(t, m.PutObject(ctx, bucket, "keep/forever", []byte("x"), "text/plain", nil))

	cfg := driver.LifecycleConfig{Rules: []driver.LifecycleRule{
		{ID: "expire-tmp", Enabled: true, Prefix: "tmp/", ExpirationDays: 7},
		{ID: "disabled-rule", Enabled: false, Prefix: "keep/", ExpirationDays: 1},
	}}
	require.NoError(t, m.PutLifecycleConfig(ctx, bucket, cfg))

	got, err := m.GetLifecycleConfig(ctx, bucket)
	require.NoError(t, err)
	require.Len(t, got.Rules, 2)
	assert.Equal(t, "expire-tmp", got.Rules[0].ID)

	t.Run("nothing expired before the horizon", func(t *testing.T) {
		clk.Advance(6 * 24 * time.Hour)

		expired, eerr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, eerr)
		assert.Empty(t, expired)
	})

	t.Run("prefix-matched objects expire after 7 days; disabled rule ignored", func(t *testing.T) {
		clk.Advance(2 * 24 * time.Hour) // now 8 days past write

		expired, eerr := m.EvaluateLifecycle(ctx, bucket)
		require.NoError(t, eerr)
		assert.Equal(t, []string{"tmp/old-1", "tmp/old-2"}, expired, "sorted, report-only")

		// evaluation does not delete
		_, gerr := m.GetObject(ctx, bucket, "tmp/old-1")
		assert.NoError(t, gerr)
	})
}

// TestPresignedURL verifies the Azure SAS-shaped URL and typed
// errors for unsupported methods.
func TestPresignedURL(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "sas-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))
	require.NoError(t, m.PutObject(ctx, bucket, "doc.txt", []byte("x"), "text/plain", nil))

	t.Run("GET SAS URL shape", func(t *testing.T) {
		u, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: bucket, Key: "doc.txt", Method: http.MethodGet, ExpiresIn: time.Hour,
		})
		require.NoError(t, err)
		assert.Contains(t, u.URL, ".blob.core.windows.net/"+bucket+"/doc.txt")
		assert.Contains(t, u.URL, "sv=")
		assert.Contains(t, u.URL, "sig=")
		assert.Contains(t, u.URL, "se=")
		assert.Contains(t, u.URL, "sp=r", "GET gets read permission")
		assert.Equal(t, http.MethodGet, u.Method)
	})

	t.Run("PUT SAS URL gets write permission", func(t *testing.T) {
		u, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: bucket, Key: "doc.txt", Method: http.MethodPut, ExpiresIn: time.Hour,
		})
		require.NoError(t, err)
		assert.Contains(t, u.URL, "sp=w")
	})

	t.Run("default expiry when unset", func(t *testing.T) {
		u, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: bucket, Key: "doc.txt", Method: http.MethodGet,
		})
		require.NoError(t, err)
		assert.False(t, u.ExpiresAt.IsZero())
	})

	t.Run("DELETE method is InvalidArgument", func(t *testing.T) {
		_, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: bucket, Key: "doc.txt", Method: http.MethodDelete,
		})
		require.Error(t, err)
		assert.True(t, cerrors.IsInvalidArgument(err), "got %v", err)
	})

	t.Run("unknown bucket is NotFound", func(t *testing.T) {
		_, err := m.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
			Bucket: "ghost", Key: "k", Method: http.MethodGet,
		})
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err))
	})
}

// TestTagging verifies object- and bucket-level tag journeys.
func TestTagging(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "tag-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))
	require.NoError(t, m.PutObject(ctx, bucket, "obj", []byte("x"), "text/plain", nil))

	t.Run("object tags: empty map when unset, full replace, delete", func(t *testing.T) {
		tags, err := m.GetObjectTagging(ctx, bucket, "obj")
		require.NoError(t, err)
		require.NotNil(t, tags)
		assert.Empty(t, tags)

		require.NoError(t, m.PutObjectTagging(ctx, bucket, "obj",
			map[string]string{"env": "prod", "team": "storage"}))

		tags, err = m.GetObjectTagging(ctx, bucket, "obj")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod", "team": "storage"}, tags)

		// replace entirely, not merge
		require.NoError(t, m.PutObjectTagging(ctx, bucket, "obj", map[string]string{"only": "one"}))

		tags, err = m.GetObjectTagging(ctx, bucket, "obj")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"only": "one"}, tags)

		require.NoError(t, m.DeleteObjectTagging(ctx, bucket, "obj"))

		tags, err = m.GetObjectTagging(ctx, bucket, "obj")
		require.NoError(t, err)
		assert.Empty(t, tags)
	})

	t.Run("object tag ops on missing object are NotFound", func(t *testing.T) {
		err := m.PutObjectTagging(ctx, bucket, "ghost", map[string]string{"a": "b"})
		assert.True(t, cerrors.IsNotFound(err))

		_, err = m.GetObjectTagging(ctx, bucket, "ghost")
		assert.True(t, cerrors.IsNotFound(err))

		err = m.DeleteObjectTagging(ctx, bucket, "ghost")
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("bucket tags: replace and delete always succeed", func(t *testing.T) {
		tags, err := m.GetBucketTagging(ctx, bucket)
		require.NoError(t, err)
		assert.Empty(t, tags)

		require.NoError(t, m.PutBucketTagging(ctx, bucket, map[string]string{"cost": "dev"}))

		tags, err = m.GetBucketTagging(ctx, bucket)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"cost": "dev"}, tags)

		require.NoError(t, m.DeleteBucketTagging(ctx, bucket))

		tags, err = m.GetBucketTagging(ctx, bucket)
		require.NoError(t, err)
		assert.Empty(t, tags)
	})
}

// TestBucketConfigs covers policy, CORS, and encryption config
// journeys including NotFound-when-unset and idempotent deletes.
func TestBucketConfigs(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "cfg-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	t.Run("bucket policy", func(t *testing.T) {
		_, err := m.GetBucketPolicy(ctx, bucket)
		require.Error(t, err)
		assert.True(t, cerrors.IsNotFound(err), "unset policy is NotFound")

		policy := driver.BucketPolicy{
			Version: "2012-10-17",
			Statements: []driver.PolicyStatement{{
				Effect: "Allow", Principal: "*",
				Actions:   []string{"s3:GetObject"},
				Resources: []string{"arn:aws:s3:::cfg-container/*"},
			}},
		}
		require.NoError(t, m.PutBucketPolicy(ctx, bucket, policy))

		got, err := m.GetBucketPolicy(ctx, bucket)
		require.NoError(t, err)
		assert.Equal(t, policy.Version, got.Version)
		require.Len(t, got.Statements, 1)
		assert.Equal(t, "Allow", got.Statements[0].Effect)

		require.NoError(t, m.DeleteBucketPolicy(ctx, bucket))
		require.NoError(t, m.DeleteBucketPolicy(ctx, bucket), "delete is idempotent")

		_, err = m.GetBucketPolicy(ctx, bucket)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("CORS config", func(t *testing.T) {
		_, err := m.GetCORSConfig(ctx, bucket)
		assert.True(t, cerrors.IsNotFound(err), "unset CORS is NotFound")

		cors := driver.CORSConfig{Rules: []driver.CORSRule{{
			AllowedOrigins: []string{"https://example.com"},
			AllowedMethods: []string{"GET", "PUT"},
			MaxAgeSeconds:  3600,
		}}}
		require.NoError(t, m.PutCORSConfig(ctx, bucket, cors))

		got, err := m.GetCORSConfig(ctx, bucket)
		require.NoError(t, err)
		require.Len(t, got.Rules, 1)
		assert.Equal(t, []string{"https://example.com"}, got.Rules[0].AllowedOrigins)

		require.NoError(t, m.DeleteCORSConfig(ctx, bucket))
		require.NoError(t, m.DeleteCORSConfig(ctx, bucket), "delete always succeeds")

		_, err = m.GetCORSConfig(ctx, bucket)
		assert.True(t, cerrors.IsNotFound(err))
	})

	t.Run("encryption config", func(t *testing.T) {
		_, err := m.GetEncryptionConfig(ctx, bucket)
		assert.True(t, cerrors.IsNotFound(err), "unset encryption is NotFound")

		require.NoError(t, m.PutEncryptionConfig(ctx, bucket, driver.EncryptionConfig{
			Enabled: true, Algorithm: "AES256",
		}))

		got, err := m.GetEncryptionConfig(ctx, bucket)
		require.NoError(t, err)
		assert.True(t, got.Enabled)
		assert.Equal(t, "AES256", got.Algorithm)
	})

	t.Run("config ops on missing bucket are NotFound", func(t *testing.T) {
		assert.True(t, cerrors.IsNotFound(m.PutBucketPolicy(ctx, "ghost", driver.BucketPolicy{})))
		assert.True(t, cerrors.IsNotFound(m.PutCORSConfig(ctx, "ghost", driver.CORSConfig{})))
		assert.True(t, cerrors.IsNotFound(m.PutEncryptionConfig(ctx, "ghost", driver.EncryptionConfig{})))
	})
}

// TestDataIsolation verifies stored data is decoupled from
// caller buffers (a real-user footgun with reused buffers).
func TestDataIsolation(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	const bucket = "iso-container"
	require.NoError(t, m.CreateBucket(ctx, bucket))

	buf := []byte("original")
	require.NoError(t, m.PutObject(ctx, bucket, "k", buf, "text/plain", nil))

	// mutate the caller's buffer after Put — stored copy must be unaffected
	copy(buf, "XXXXXXXX")

	got, err := m.GetObject(ctx, bucket, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), got.Data, "PutObject stores a defensive copy of data")

	// mutate the returned buffer — stored copy must be unaffected
	copy(got.Data, "YYYYYYYY")

	got2, err := m.GetObject(ctx, bucket, "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), got2.Data, "GetObject returns a defensive copy of data")
}
