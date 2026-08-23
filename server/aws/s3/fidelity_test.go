package s3_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3 ETags are MD5 digests; the test recomputes one to assert the shape
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKListParts covers #266: ListParts now reports the parts buffered so
// far (ordered by part number) instead of an empty list, so resumable-upload
// tooling can reconstruct its state.
func TestSDKListParts(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "lp-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	uploadID := aws.ToString(created.UploadId)

	// Upload part 2 before part 1 to confirm ListParts sorts by part number.
	sizes := map[int32]int{1: 1024, 2: 2048}
	for _, pn := range []int32{2, 1} {
		if _, err := client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
			PartNumber: aws.Int32(pn), Body: bytes.NewReader(bytes.Repeat([]byte("x"), sizes[pn])),
		}); err != nil {
			t.Fatalf("UploadPart %d: %v", pn, err)
		}
	}

	out, err := client.ListParts(ctx, &awss3.ListPartsInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(out.Parts) != 2 {
		t.Fatalf("ListParts returned %d parts, want 2", len(out.Parts))
	}
	if aws.ToInt32(out.Parts[0].PartNumber) != 1 || aws.ToInt32(out.Parts[1].PartNumber) != 2 {
		t.Fatalf("parts not sorted ascending: got %d then %d",
			aws.ToInt32(out.Parts[0].PartNumber), aws.ToInt32(out.Parts[1].PartNumber))
	}
	if aws.ToInt64(out.Parts[0].Size) != 1024 || aws.ToInt64(out.Parts[1].Size) != 2048 {
		t.Fatalf("part sizes = %d, %d; want 1024, 2048",
			aws.ToInt64(out.Parts[0].Size), aws.ToInt64(out.Parts[1].Size))
	}
	for _, p := range out.Parts {
		if aws.ToString(p.ETag) == "" {
			t.Fatalf("part %d has empty ETag", aws.ToInt32(p.PartNumber))
		}
	}
}

// TestSDKUploadPartNumberOutOfRange covers #266: part numbers must be within
// 1..10000; anything larger is rejected with InvalidArgument.
func TestSDKUploadPartNumberOutOfRange(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "pn-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	uploadID := aws.ToString(created.UploadId)

	_, err = client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(10001), Body: bytes.NewReader([]byte("x")),
	})
	if err == nil {
		t.Fatal("UploadPart with partNumber 10001 succeeded, want InvalidArgument")
	}
	if !strings.Contains(err.Error(), "InvalidArgument") {
		t.Fatalf("error = %v, want InvalidArgument", err)
	}
}

// TestSubResourceMethodNotAllowed covers #266: a non-GET request carrying the
// ?uploads/?versions sub-resource must be rejected (405), not silently routed
// to create/delete-bucket (which ignored the sub-resource).
func TestSubResourceMethodNotAllowed(t *testing.T) {
	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{S3: cloud.S3}))
	t.Cleanup(ts.Close)

	status := func(method, path string) int {
		req, err := http.NewRequest(method, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// PUT /{bucket}?uploads previously fell through to CreateBucket (200).
	if code := status(http.MethodPut, "/b?uploads"); code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT ?uploads status = %d, want 405", code)
	}
	if code := status(http.MethodDelete, "/b?versions"); code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE ?versions status = %d, want 405", code)
	}
	// The bucket must NOT have been created by the misrouted PUT above.
	if code := status(http.MethodGet, "/b"); code == http.StatusOK {
		t.Fatal("bucket 'b' exists — a ?uploads PUT was misrouted to CreateBucket")
	}
}

// TestSDKGetObjectRange covers the Range-header gap: GetObject(Range=bytes=0-4)
// must return 206 Partial Content with a Content-Range header and just the
// requested byte slice, not the full body with 200.
func TestSDKGetObjectRange(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "range-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("hello world")),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	out, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=0-4"),
	})
	if err != nil {
		t.Fatalf("GetObject(Range): %v", err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if string(body) != "hello" {
		t.Fatalf("ranged body = %q, want %q", string(body), "hello")
	}
	if aws.ToString(out.ContentRange) != "bytes 0-4/11" {
		t.Fatalf("ContentRange = %q, want %q", aws.ToString(out.ContentRange), "bytes 0-4/11")
	}
	if aws.ToInt64(out.ContentLength) != 5 {
		t.Fatalf("ContentLength = %d, want 5", aws.ToInt64(out.ContentLength))
	}

	// A suffix range returns the trailing bytes.
	suffix, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=-5"),
	})
	if err != nil {
		t.Fatalf("GetObject(suffix range): %v", err)
	}
	defer suffix.Body.Close()

	sb, _ := io.ReadAll(suffix.Body)
	if string(sb) != "world" {
		t.Fatalf("suffix ranged body = %q, want %q", string(sb), "world")
	}
}

// TestSDKObjectETagIsMD5 covers the ETag-algorithm gap: a single-part PutObject
// ETag must be the 32-char MD5 hex of the body, not a 64-char SHA-256.
func TestSDKObjectETagIsMD5(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "etag-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	put, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("hello world")),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	sum := md5.Sum([]byte("hello world")) //nolint:gosec // asserting S3's MD5 ETag shape
	wantETag := hex.EncodeToString(sum[:])
	if wantETag != "5eb63bbbe01eeed093cb22bb8f5acdc3" {
		t.Fatalf("test vector wrong: %q", wantETag)
	}

	gotETag := strings.Trim(aws.ToString(put.ETag), `"`)
	if gotETag != wantETag {
		t.Fatalf("PutObject ETag = %q, want MD5 %q", gotETag, wantETag)
	}
	if len(gotETag) != 32 {
		t.Fatalf("ETag length = %d, want 32 (MD5 hex)", len(gotETag))
	}
}

// TestSDKMultipartCompleteETagSuffix covers the multipart-ETag gap: a completed
// multipart object's ETag must carry the "-<numParts>" suffix so tooling can
// detect it was assembled from parts.
func TestSDKMultipartCompleteETagSuffix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "mp-etag-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	uploadID := aws.ToString(created.UploadId)

	parts := make([]types.CompletedPart, 0, 2)
	for i, data := range [][]byte{bytes.Repeat([]byte("A"), 1024), bytes.Repeat([]byte("B"), 2048)} {
		pn := int32(i + 1) //nolint:gosec // small loop index
		up, upErr := client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
			PartNumber: aws.Int32(pn), Body: bytes.NewReader(data),
		})
		if upErr != nil {
			t.Fatalf("UploadPart %d: %v", pn, upErr)
		}
		parts = append(parts, types.CompletedPart{ETag: up.ETag, PartNumber: aws.Int32(pn)})
	}

	done, err := client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	etag := strings.Trim(aws.ToString(done.ETag), `"`)
	if !strings.HasSuffix(etag, "-2") {
		t.Fatalf("multipart ETag = %q, want a %q suffix", etag, "-2")
	}
}

// TestSDKListObjectsV2KeyCount covers the KeyCount gap: with a delimiter,
// KeyCount must count returned keys AND common prefixes, not just Contents.
func TestSDKListObjectsV2KeyCount(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "kc-bucket"
	mustCreateBucket(t, client, bucket)

	// Two top-level keys + three keys under two "directory" prefixes.
	keys := []string{"a.txt", "b.txt", "docs/1", "docs/2", "img/1"}
	for _, k := range keys {
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(k), Body: bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", k, err)
		}
	}

	out, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket), Delimiter: aws.String("/"),
	})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}

	// 2 top-level keys (Contents) + 2 common prefixes (docs/, img/) = 4.
	if len(out.Contents) != 2 || len(out.CommonPrefixes) != 2 {
		t.Fatalf("Contents=%d CommonPrefixes=%d, want 2 and 2", len(out.Contents), len(out.CommonPrefixes))
	}
	if aws.ToInt32(out.KeyCount) != 4 {
		t.Fatalf("KeyCount = %d, want 4 (Contents + CommonPrefixes)", aws.ToInt32(out.KeyCount))
	}
}

// TestSDKListBucketsOwner covers the missing-Owner gap: ListBuckets must carry
// an <Owner> element so clients that read the bucket owner don't see nil.
func TestSDKListBucketsOwner(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "owned-bucket")

	out, err := client.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	if out.Owner == nil || aws.ToString(out.Owner.ID) == "" {
		t.Fatalf("ListBuckets Owner = %+v, want a non-empty owner", out.Owner)
	}
}
