// s3_lifecycle_test.go — //
// Real-user-journey  tests that drive the genuine aws-sdk-go-v2 S3 client
// against the emulator's HTTP server (httptest). Assertions are made on
// SDK-decoded responses and SDK-visible error types, not raw HTTP.
package s3_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"net/http/httptest"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newTestServer stands up the emulator's AWS server with the drivers this
// suite exercises and returns its URL plus a ready SDK config.
func newTestServer(t *testing.T) (string, aws.Config) {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		S3:       provider.S3,
		DynamoDB: provider.DynamoDB,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	return ts.URL, cfg
}

// newSuiteS3Client builds a real S3 SDK client pointed at a fresh emulator
// instance. Retries are disabled so error-path assertions observe exactly one
// attempt (the emulator maps some driver errors to HTTP 500, which the default
// retryer would otherwise retry with backoff).
func newSuiteS3Client(t *testing.T) *s3.Client {
	t.Helper()

	url, cfg := newTestServer(t)

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(url)
		o.UsePathStyle = true
		o.Retryer = aws.NopRetryer{}
	})
}

func suiteCreateBucket(t *testing.T, client *s3.Client, bucket string) {
	t.Helper()

	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err, "CreateBucket %q", bucket)
}

func suitePut(t *testing.T, client *s3.Client, bucket, key string, body []byte, contentType string) {
	t.Helper()

	in := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}

	_, err := client.PutObject(context.Background(), in)
	require.NoError(t, err, "PutObject %s/%s", bucket, key)
}

func suiteGetBody(t *testing.T, client *s3.Client, bucket, key string) ([]byte, *s3.GetObjectOutput) {
	t.Helper()

	out, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	require.NoError(t, err, "GetObject %s/%s", bucket, key)

	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	require.NoError(t, err, "read body %s/%s", bucket, key)

	return data, out
}

func quotedSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// TestS3FullLifecycle walks the full user journey: create bucket,
// put objects with varied content types (including an empty body and a ~1MB
// binary payload), get, head, list, copy to a second bucket, delete objects,
// delete both buckets, and verify the buckets are gone.
func TestS3ObjectJourney(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-lifecycle"
	const copyBucket = "e2e-lifecycle-copy"

	suiteCreateBucket(t, client, bucket)
	suiteCreateBucket(t, client, copyBucket)

	oneMB := bytes.Repeat([]byte("cloudemu-1mb-payload!"), (1<<20)/21+1)[:1<<20]

	objects := []struct {
		key         string
		body        []byte
		contentType string
	}{
		{"docs/readme.txt", []byte("hello e2e suite"), "text/plain"},
		{"docs/config.json", []byte(`{"a":1}`), "application/json"},
		{"blobs/empty.bin", []byte{}, "application/octet-stream"},
		{"blobs/big.bin", oneMB, "application/octet-stream"},
	}

	for _, obj := range objects {
		suitePut(t, client, bucket, obj.key, obj.body, obj.contentType)
	}

	// Get: bodies, content types, and the emulator's sha256-hex ETag (note:
	// real S3 uses MD5-based ETags; the emulator documents sha256).
	for _, obj := range objects {
		data, out := suiteGetBody(t, client, bucket, obj.key)
		assert.Equal(t, obj.body, data, "body %s", obj.key)
		assert.Equal(t, obj.contentType, aws.ToString(out.ContentType), "content-type %s", obj.key)
		assert.Equal(t, quotedSHA256(obj.body), aws.ToString(out.ETag), "etag %s", obj.key)
		assert.Equal(t, int64(len(obj.body)), aws.ToInt64(out.ContentLength), "content-length %s", obj.key)
	}

	// Head: metadata only, size and ETag must match without a body transfer.
	head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("blobs/big.bin"),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1<<20), aws.ToInt64(head.ContentLength))
	assert.Equal(t, quotedSHA256(oneMB), aws.ToString(head.ETag))
	assert.NotNil(t, head.LastModified)

	// List everything: keys come back lexically sorted.
	list, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.Len(t, list.Contents, len(objects))

	gotKeys := make([]string, 0, len(list.Contents))
	for _, c := range list.Contents {
		gotKeys = append(gotKeys, aws.ToString(c.Key))
	}
	assert.Equal(t,
		[]string{"blobs/big.bin", "blobs/empty.bin", "docs/config.json", "docs/readme.txt"},
		gotKeys, "list must be lexically sorted")
	assert.False(t, aws.ToBool(list.IsTruncated))

	// Copy cross-bucket: ETag is preserved from the source.
	src, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("docs/readme.txt"),
	})
	require.NoError(t, err)

	copied, err := client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(copyBucket),
		Key:        aws.String("copied/readme.txt"),
		CopySource: aws.String(bucket + "/docs/readme.txt"),
	})
	require.NoError(t, err)
	require.NotNil(t, copied.CopyObjectResult)
	assert.Equal(t, aws.ToString(src.ETag), aws.ToString(copied.CopyObjectResult.ETag),
		"copy must preserve source ETag")

	copyBody, _ := suiteGetBody(t, client, copyBucket, "copied/readme.txt")
	assert.Equal(t, []byte("hello e2e suite"), copyBody)

	// Delete all objects, then the buckets.
	for _, obj := range objects {
		_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(obj.key),
		})
		require.NoError(t, err, "DeleteObject %s", obj.key)
	}

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(copyBucket), Key: aws.String("copied/readme.txt"),
	})
	require.NoError(t, err)

	empty, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	assert.Empty(t, empty.Contents, "bucket should be empty before delete")

	for _, b := range []string{bucket, copyBucket} {
		_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(b)})
		require.NoError(t, err, "DeleteBucket %q", b)
	}

	buckets, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)
	for _, b := range buckets.Buckets {
		assert.NotContains(t, []string{bucket, copyBucket}, aws.ToString(b.Name),
			"deleted bucket still listed")
	}
}

// TestS3ListPrefixDelimiter exercises prefix + delimiter roll-up
// into CommonPrefixes, the way a console-style folder browser would.
func TestS3ListPrefixDelimiter(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-prefix-delim"
	suiteCreateBucket(t, client, bucket)

	keys := []string{
		"logs/2025/app.log",
		"logs/2026/app.log",
		"logs/root.log",
		"data/x.bin",
		"top.txt",
	}
	for _, k := range keys {
		suitePut(t, client, bucket, k, []byte(k), "text/plain")
	}

	// Root listing with "/": two folders plus one top-level file.
	root, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String("/"),
	})
	require.NoError(t, err)

	prefixes := make([]string, 0, len(root.CommonPrefixes))
	for _, p := range root.CommonPrefixes {
		prefixes = append(prefixes, aws.ToString(p.Prefix))
	}
	assert.Equal(t, []string{"data/", "logs/"}, prefixes)

	require.Len(t, root.Contents, 1)
	assert.Equal(t, "top.txt", aws.ToString(root.Contents[0].Key))

	// Descend into logs/: year folders roll up, root.log stays a key.
	logs, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Prefix:    aws.String("logs/"),
		Delimiter: aws.String("/"),
	})
	require.NoError(t, err)

	logPrefixes := make([]string, 0, len(logs.CommonPrefixes))
	for _, p := range logs.CommonPrefixes {
		logPrefixes = append(logPrefixes, aws.ToString(p.Prefix))
	}
	assert.Equal(t, []string{"logs/2025/", "logs/2026/"}, logPrefixes)

	require.Len(t, logs.Contents, 1)
	assert.Equal(t, "logs/root.log", aws.ToString(logs.Contents[0].Key))

	// Prefix with no matches: empty result, not an error.
	none, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String("nope/"),
	})
	require.NoError(t, err)
	assert.Empty(t, none.Contents)
	assert.Empty(t, none.CommonPrefixes)
}

// TestS3PaginationContinuationTokens uploads more than the default
// page size (1000) and pages through with the SDK's continuation token. The
// handler does not parse max-keys, so the only way to trigger truncation is to
// exceed the 1000-key default page.
func TestS3PaginationContinuationTokens(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-pagination"
	const total = 1005

	suiteCreateBucket(t, client, bucket)

	for i := 0; i < total; i++ {
		suitePut(t, client, bucket, fmt.Sprintf("obj-%04d", i), []byte{byte(i)}, "")
	}

	page1, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	require.Len(t, page1.Contents, 1000, "first page should be the 1000-key default")
	assert.True(t, aws.ToBool(page1.IsTruncated))
	require.NotEmpty(t, aws.ToString(page1.NextContinuationToken))
	assert.Equal(t, "obj-0000", aws.ToString(page1.Contents[0].Key))
	assert.Equal(t, "obj-0999", aws.ToString(page1.Contents[999].Key))

	page2, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:            aws.String(bucket),
		ContinuationToken: page1.NextContinuationToken,
	})
	require.NoError(t, err)

	require.Len(t, page2.Contents, total-1000)
	assert.False(t, aws.ToBool(page2.IsTruncated))
	assert.Empty(t, aws.ToString(page2.NextContinuationToken))
	assert.Equal(t, "obj-1000", aws.ToString(page2.Contents[0].Key))
	assert.Equal(t, "obj-1004", aws.ToString(page2.Contents[len(page2.Contents)-1].Key))

	// No overlap between pages.
	seen := make(map[string]struct{}, total)
	for _, c := range append(page1.Contents, page2.Contents...) {
		k := aws.ToString(c.Key)
		_, dup := seen[k]
		require.False(t, dup, "key %q returned on both pages", k)
		seen[k] = struct{}{}
	}
	assert.Len(t, seen, total)
}

// TestS3GetMissingObjectTypedError asserts GetObject on an absent
// key surfaces the SDK's typed *types.NoSuchKey error.
func TestS3GetMissingObjectTypedError(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-err-get"
	suiteCreateBucket(t, client, bucket)

	_, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("does-not-exist"),
	})
	require.Error(t, err)

	var nsk *types.NoSuchKey
	assert.True(t, errors.As(err, &nsk), "want *types.NoSuchKey, got %T: %v", err, err)
}

// TestS3HeadMissingObjectTypedError asserts HeadObject on an absent
// key surfaces *types.NotFound (HEAD has no body, so the SDK maps the 404
// status).
func TestS3HeadMissingObjectTypedError(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-err-head"
	suiteCreateBucket(t, client, bucket)

	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("missing"),
	})
	require.Error(t, err)

	var nf *types.NotFound
	assert.True(t, errors.As(err, &nf), "want *types.NotFound, got %T: %v", err, err)
}

// TestS3DeleteMissingObjectIdempotent asserts real-S3 semantics: deleting
// an absent key succeeds (204) so defensive-delete SDK code just works,
// while a missing BUCKET is still NoSuchBucket.
func TestS3DeleteMissingObjectIdempotent(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-err-del"
	suiteCreateBucket(t, client, bucket)

	_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("never-put"),
	})
	require.NoError(t, err, "real S3 DeleteObject is idempotent for a missing key")

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String("ghost-bucket"),
		Key:    aws.String("k"),
	})
	require.Error(t, err, "missing bucket must still fail")

	var apiErr smithy.APIError
	require.True(t, errors.As(err, &apiErr), "want smithy.APIError, got %T: %v", err, err)
	assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
}

// TestS3MissingBucketError asserts GetObject against a bucket that
// was never created fails with NoSuchBucket, matching real S3.
func TestS3MissingBucketError(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	_, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("never-created"),
		Key:    aws.String("any"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.True(t, errors.As(err, &apiErr), "want smithy.APIError, got %T: %v", err, err)
	assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
}

// TestS3DuplicateBucketTypedError asserts the second CreateBucket
// for the same name surfaces *types.BucketAlreadyOwnedByYou.
func TestS3DuplicateBucketTypedError(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-dup-bucket"
	suiteCreateBucket(t, client, bucket)

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)

	var owned *types.BucketAlreadyOwnedByYou
	assert.True(t, errors.As(err, &owned),
		"want *types.BucketAlreadyOwnedByYou, got %T: %v", err, err)
}

// TestS3DeleteNonEmptyBucketError asserts deleting a non-empty
// bucket fails with 409 BucketNotEmpty (real-S3 semantics) and the
// bucket's contents survive.
func TestS3DeleteNonEmptyBucketError(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-nonempty"
	suiteCreateBucket(t, client, bucket)
	suitePut(t, client, bucket, "keep.txt", []byte("still here"), "text/plain")

	_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err, "deleting a non-empty bucket must fail")

	var apiErr smithy.APIError
	require.True(t, errors.As(err, &apiErr), "want smithy.APIError, got %T: %v", err, err)
	assert.Equal(t, "BucketNotEmpty", apiErr.ErrorCode())

	// Bucket and object survive the failed delete.
	body, _ := suiteGetBody(t, client, bucket, "keep.txt")
	assert.Equal(t, []byte("still here"), body)
}

// TestS3ListEmptyBucket asserts listing a freshly created bucket
// succeeds with zero contents and zero common prefixes.
func TestS3ListEmptyBucket(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-empty-list"
	suiteCreateBucket(t, client, bucket)

	out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Empty(t, out.Contents)
	assert.Empty(t, out.CommonPrefixes)
	assert.False(t, aws.ToBool(out.IsTruncated))
	assert.Equal(t, int32(0), aws.ToInt32(out.KeyCount))
}

// TestS3KeysWithSlashesAndUnicode round-trips keys containing
// nested slashes, unicode (CJK, accents, emoji), spaces, and '+' through
// put/head/get/list/delete via the real SDK's URL escaping.
func TestS3KeysWithSlashesAndUnicode(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-fancy-keys"
	suiteCreateBucket(t, client, bucket)

	keys := []string{
		"deep/nested/path/file.txt",
		"报告/月度/结果.txt",
		"résumé-ünïcode.pdf",
		"emoji/🚀-launch plan.md",
		"spaces and+plus.txt",
	}

	for i, k := range keys {
		suitePut(t, client, bucket, k, []byte(fmt.Sprintf("payload-%d", i)), "text/plain")
	}

	for i, k := range keys {
		head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(k),
		})
		require.NoError(t, err, "HeadObject %q", k)
		assert.Equal(t, int64(len(fmt.Sprintf("payload-%d", i))), aws.ToInt64(head.ContentLength))

		body, _ := suiteGetBody(t, client, bucket, k)
		assert.Equal(t, []byte(fmt.Sprintf("payload-%d", i)), body, "body for key %q", k)
	}

	list, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	listed := make(map[string]bool, len(list.Contents))
	for _, c := range list.Contents {
		listed[aws.ToString(c.Key)] = true
	}
	for _, k := range keys {
		assert.True(t, listed[k], "key %q missing from listing (got %v)", k, listed)
	}

	for _, k := range keys {
		_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(k),
		})
		require.NoError(t, err, "DeleteObject %q", k)
	}

	after, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	assert.Empty(t, after.Contents)
}

// TestS3MetadataContentTypeRoundTrip verifies x-amz-meta-* user
// metadata and Content-Type survive PutObject -> HeadObject/GetObject. Keys
// are lowercase because HTTP header canonicalization lowercases them anyway.
func TestS3MetadataContentTypeRoundTrip(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-metadata"
	const key = "with-meta.csv"

	suiteCreateBucket(t, client, bucket)

	meta := map[string]string{
		"owner":    "suite",
		"pipeline": "e2e-full-matrix",
	}

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte("a,b\n1,2\n")),
		ContentType: aws.String("text/csv"),
		Metadata:    meta,
	})
	require.NoError(t, err)

	head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	assert.Equal(t, "text/csv", aws.ToString(head.ContentType))
	assert.Equal(t, meta, head.Metadata, "HeadObject metadata round-trip")

	_, get := suiteGetBody(t, client, bucket, key)
	assert.Equal(t, meta, get.Metadata, "GetObject metadata round-trip")
}

// TestS3CopyObjectSemantics verifies the survey-documented copy
// behavior end-to-end: ETag and metadata are preserved, tags are NOT copied,
// and copying from a missing source key fails with NoSuchKey.
func TestS3CopyObjectSemantics(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const srcBucket = "e2e-copy-src"
	const dstBucket = "e2e-copy-dst"

	suiteCreateBucket(t, client, srcBucket)
	suiteCreateBucket(t, client, dstBucket)

	body := []byte("copy me")
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(srcBucket),
		Key:         aws.String("src.txt"),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("text/plain"),
		Metadata:    map[string]string{"origin": "src"},
	})
	require.NoError(t, err)

	_, err = client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(srcBucket),
		Key:    aws.String("src.txt"),
		Tagging: &types.Tagging{TagSet: []types.Tag{
			{Key: aws.String("classified"), Value: aws.String("yes")},
		}},
	})
	require.NoError(t, err)

	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String("dst.txt"),
		CopySource: aws.String(srcBucket + "/src.txt"),
	})
	require.NoError(t, err)

	gotBody, get := suiteGetBody(t, client, dstBucket, "dst.txt")
	assert.Equal(t, body, gotBody)
	assert.Equal(t, quotedSHA256(body), aws.ToString(get.ETag), "copy preserves ETag")
	assert.Equal(t, "text/plain", aws.ToString(get.ContentType), "copy preserves content type")
	assert.Equal(t, map[string]string{"origin": "src"}, get.Metadata, "copy preserves metadata")

	dstTags, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(dstBucket), Key: aws.String("dst.txt"),
	})
	require.NoError(t, err)
	assert.Empty(t, dstTags.TagSet, "tags must NOT be copied")

	// Copy from a missing source key fails.
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String("never.txt"),
		CopySource: aws.String(srcBucket + "/missing.txt"),
	})
	require.Error(t, err, "copying a missing source key must fail")
}

// TestS3OverwriteResetsTags verifies overwriting a key via
// PutObject replaces the whole object — new body, new content type, and the
// previous tag set is dropped (fresh object, Tags nil).
func TestS3OverwriteResetsTags(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-overwrite"
	const key = "mutable.txt"

	suiteCreateBucket(t, client, bucket)
	suitePut(t, client, bucket, key, []byte("v1"), "text/plain")

	_, err := client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Tagging: &types.Tagging{TagSet: []types.Tag{
			{Key: aws.String("version"), Value: aws.String("one")},
		}},
	})
	require.NoError(t, err)

	suitePut(t, client, bucket, key, []byte("v2 body is longer"), "application/json")

	body, get := suiteGetBody(t, client, bucket, key)
	assert.Equal(t, []byte("v2 body is longer"), body)
	assert.Equal(t, "application/json", aws.ToString(get.ContentType))
	assert.Equal(t, quotedSHA256([]byte("v2 body is longer")), aws.ToString(get.ETag))

	tags, err := client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	assert.Empty(t, tags.TagSet, "overwrite must drop the old tag set")
}

// TestS3VersioningAndListVersions verifies the boolean-flag
// versioning model over the SDK: fresh bucket reports empty status, enabling
// flips it to Enabled, and ListObjectVersions exposes only the latest state of
// each key as a single "null" version (the driver keeps no history).
func TestS3VersioningAndListVersions(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-versioning"
	suiteCreateBucket(t, client, bucket)

	initial, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Empty(t, initial.Status, "fresh bucket has no versioning status")

	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	enabled, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Equal(t, types.BucketVersioningStatusEnabled, enabled.Status)

	// Even with versioning "enabled", overwrites keep no history: a key has
	// exactly one version with the null version id.
	suitePut(t, client, bucket, "doc.txt", []byte("v1"), "text/plain")
	suitePut(t, client, bucket, "doc.txt", []byte("v2"), "text/plain")

	versions, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.Len(t, versions.Versions, 1, "flag-only versioning keeps no history")

	v := versions.Versions[0]
	assert.Equal(t, "doc.txt", aws.ToString(v.Key))
	assert.Equal(t, "null", aws.ToString(v.VersionId))
	assert.True(t, aws.ToBool(v.IsLatest))
	assert.Equal(t, quotedSHA256([]byte("v2")), aws.ToString(v.ETag),
		"the sole version reflects the latest overwrite")
}

// TestS3ListBucketsSorted verifies ListBuckets returns all buckets
// sorted by name regardless of creation order.
func TestS3ListBucketsSorted(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	for _, b := range []string{"zeta-bucket", "alpha-bucket", "mid-bucket"} {
		suiteCreateBucket(t, client, b)
	}

	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)
	require.Len(t, out.Buckets, 3)

	names := make([]string, 0, 3)
	for _, b := range out.Buckets {
		names = append(names, aws.ToString(b.Name))
		assert.NotNil(t, b.CreationDate, "bucket %q missing CreationDate", aws.ToString(b.Name))
	}
	assert.Equal(t, []string{"alpha-bucket", "mid-bucket", "zeta-bucket"}, names)
}

// TestS3PutObjectReturnsETag asserts the S3-standard contract that
// a PutObject response carries the object's ETag. Real S3 always returns an
// ETag header on PUT, and SDK users routinely read resp.ETag to verify or
// record uploads. The emulator's putObject handler responds 200 with no ETag
// header, so this is expected to FAIL until the handler sets it
// (server/aws/s3/handler.go putObject).
func TestS3PutObjectReturnsETag(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "e2e-put-etag"
	suiteCreateBucket(t, client, bucket)

	body := []byte("etag please")

	put, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("obj.txt"),
		Body:   bytes.NewReader(body),
	})
	require.NoError(t, err)

	assert.Equal(t, quotedSHA256(body), aws.ToString(put.ETag),
		"real S3 returns the object ETag on PutObject; the emulator omits it")
}
