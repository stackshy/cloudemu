package s3_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3 ETags are defined as MD5 digests, not a security primitive
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// putObject is a small helper for the copy tests.
func putObject(t *testing.T, c *awss3.Client, bucket, key, body, contentType string, meta map[string]string) {
	t.Helper()

	in := &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		Body: bytes.NewReader([]byte(body)), Metadata: meta,
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}

	if _, err := c.PutObject(context.Background(), in); err != nil {
		t.Fatalf("PutObject %s/%s: %v", bucket, key, err)
	}
}

func getBody(t *testing.T, c *awss3.Client, bucket, key string) string {
	t.Helper()

	out, err := c.GetObject(context.Background(), &awss3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObject %s/%s: %v", bucket, key, err)
	}
	defer out.Body.Close()

	b, _ := io.ReadAll(out.Body)

	return string(b)
}

// TestSDKUploadPartCopy drives the high-level copy path: part 2 of a multipart
// upload is filled by copying an existing object via UploadPartCopy.
func TestSDKUploadPartCopy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "upc")
	putObject(t, client, "upc", "src", "COPIED-PART-DATA", "", nil)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String("upc"), Key: aws.String("dst"),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	uploadID := aws.ToString(created.UploadId)

	part1 := bytes.Repeat([]byte("A"), 8)
	up1, err := client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String("upc"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(1), Body: bytes.NewReader(part1),
	})
	if err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	upc, err := client.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
		Bucket: aws.String("upc"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(2), CopySource: aws.String("upc/src"),
	})
	if err != nil {
		t.Fatalf("UploadPartCopy: %v", err)
	}

	if upc.CopyPartResult == nil || aws.ToString(upc.CopyPartResult.ETag) == "" {
		t.Fatalf("UploadPartCopy returned no CopyPartResult ETag: %+v", upc.CopyPartResult)
	}

	_, err = client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String("upc"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{
			{ETag: up1.ETag, PartNumber: aws.Int32(1)},
			{ETag: upc.CopyPartResult.ETag, PartNumber: aws.Int32(2)},
		}},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	if got := getBody(t, client, "upc", "dst"); got != "AAAAAAAACOPIED-PART-DATA" {
		t.Fatalf("assembled object = %q, want AAAAAAAACOPIED-PART-DATA", got)
	}
}

// TestSDKUploadPartCopyRange reconstructs an object from two byte-range part
// copies, verifying x-amz-copy-source-range is honored exactly.
func TestSDKUploadPartCopyRange(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "upcr")
	putObject(t, client, "upcr", "src", "0123456789ABCDEFGHIJ", "", nil) // 20 bytes

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String("upcr"), Key: aws.String("dst"),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	uploadID := aws.ToString(created.UploadId)

	ranges := []string{"bytes=0-9", "bytes=10-19"}
	parts := make([]types.CompletedPart, 0, len(ranges))

	for i, rng := range ranges {
		partNum := int32(i + 1) //nolint:gosec // small loop index

		out, cErr := client.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
			Bucket: aws.String("upcr"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
			PartNumber: aws.Int32(partNum), CopySource: aws.String("upcr/src"),
			CopySourceRange: aws.String(rng),
		})
		if cErr != nil {
			t.Fatalf("UploadPartCopy %s: %v", rng, cErr)
		}

		parts = append(parts, types.CompletedPart{ETag: out.CopyPartResult.ETag, PartNumber: aws.Int32(partNum)})
	}

	if _, err = client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String("upcr"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	}); err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	if got := getBody(t, client, "upcr", "dst"); got != "0123456789ABCDEFGHIJ" {
		t.Fatalf("range-assembled object = %q, want the full source", got)
	}
}

// TestSDKCopyObjectMetadataDirective covers COPY (carry source metadata) vs
// REPLACE (take request metadata + content-type).
func TestSDKCopyObjectMetadataDirective(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "mdb")
	putObject(t, client, "mdb", "src", "payload", "text/plain", map[string]string{"origin": "source"})

	// Default directive COPY carries the source metadata + content type.
	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("mdb"), Key: aws.String("copied"), CopySource: aws.String("mdb/src"),
	}); err != nil {
		t.Fatalf("CopyObject (COPY): %v", err)
	}

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("mdb"), Key: aws.String("copied")})
	if err != nil {
		t.Fatalf("HeadObject (copied): %v", err)
	}

	if head.Metadata["origin"] != "source" || aws.ToString(head.ContentType) != "text/plain" {
		t.Fatalf("COPY did not carry metadata/content-type: meta=%v ct=%q", head.Metadata, aws.ToString(head.ContentType))
	}

	// REPLACE overrides metadata and content type from the request.
	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("mdb"), Key: aws.String("replaced"), CopySource: aws.String("mdb/src"),
		MetadataDirective: types.MetadataDirectiveReplace,
		Metadata:          map[string]string{"origin": "request"},
		ContentType:       aws.String("application/json"),
	}); err != nil {
		t.Fatalf("CopyObject (REPLACE): %v", err)
	}

	head, err = client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("mdb"), Key: aws.String("replaced")})
	if err != nil {
		t.Fatalf("HeadObject (replaced): %v", err)
	}

	if head.Metadata["origin"] != "request" || aws.ToString(head.ContentType) != "application/json" {
		t.Fatalf("REPLACE did not apply request metadata/content-type: meta=%v ct=%q",
			head.Metadata, aws.ToString(head.ContentType))
	}
}

// TestSDKCopyObjectVersionedSource copies a specific (non-current) source
// version by appending ?versionId to the copy source.
func TestSDKCopyObjectVersionedSource(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "vsrc")

	if _, err := client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String("vsrc"),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	v1, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("vsrc"), Key: aws.String("k"), Body: bytes.NewReader([]byte("one")),
	})
	if err != nil {
		t.Fatalf("PutObject v1: %v", err)
	}

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("vsrc"), Key: aws.String("k"), Body: bytes.NewReader([]byte("two")),
	}); err != nil {
		t.Fatalf("PutObject v2: %v", err)
	}

	oldVersion := aws.ToString(v1.VersionId)
	if oldVersion == "" {
		t.Fatal("first PutObject returned no version id")
	}

	out, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("vsrc"), Key: aws.String("restored"),
		CopySource: aws.String("vsrc/k?versionId=" + oldVersion),
	})
	if err != nil {
		t.Fatalf("CopyObject (versioned source): %v", err)
	}

	if aws.ToString(out.CopySourceVersionId) != oldVersion {
		t.Fatalf("CopySourceVersionId = %q, want %q", aws.ToString(out.CopySourceVersionId), oldVersion)
	}

	if got := getBody(t, client, "vsrc", "restored"); got != "one" {
		t.Fatalf("versioned copy body = %q, want the older version 'one'", got)
	}
}

// TestSDKCopyObjectConditional verifies copy-source preconditions: a mismatched
// if-match returns 412 and does not copy; a matching one succeeds.
func TestSDKCopyObjectConditional(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "cond")
	putObject(t, client, "cond", "src", "data", "", nil)

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("cond"), Key: aws.String("src")})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	etag := aws.ToString(head.ETag)

	_, err = client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("cond"), Key: aws.String("dst"), CopySource: aws.String("cond/src"),
		CopySourceIfMatch: aws.String("\"0000000000000000000000000000dead\""),
	})
	assertAPICode(t, err, "PreconditionFailed")
	assertStatus(t, err, 412)

	// The failed precondition must not have created the destination object.
	if _, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String("cond"), Key: aws.String("dst"),
	}); err == nil {
		t.Fatal("destination object exists after a failed-precondition copy")
	}

	// A matching if-match succeeds.
	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("cond"), Key: aws.String("dst"), CopySource: aws.String("cond/src"),
		CopySourceIfMatch: aws.String(etag),
	}); err != nil {
		t.Fatalf("CopyObject (matching if-match): %v", err)
	}

	// A matching if-none-match must fail with 412.
	_, err = client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("cond"), Key: aws.String("dst2"), CopySource: aws.String("cond/src"),
		CopySourceIfNoneMatch: aws.String(etag),
	})
	assertAPICode(t, err, "PreconditionFailed")
}

// TestSDKCopyObjectIfMatchOverridesUnmodifiedSince verifies AWS's documented
// combined-precedence override: with both x-amz-copy-source-if-match and
// x-amz-copy-source-if-unmodified-since present and if-match true, S3 returns
// 200 OK and copies even though if-unmodified-since evaluates false (the source
// was modified after the supplied time). The two headers are not independent.
func TestSDKCopyObjectIfMatchOverridesUnmodifiedSince(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "ovr")
	putObject(t, client, "ovr", "src", "data", "", nil)

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("ovr"), Key: aws.String("src")})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	// A time before the source's last modification: if-unmodified-since alone
	// evaluates false (the source was modified since) and would fail with 412.
	past := aws.ToTime(head.LastModified).Add(-1 * time.Hour)

	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("ovr"), Key: aws.String("dst"), CopySource: aws.String("ovr/src"),
		CopySourceIfMatch:           head.ETag,
		CopySourceIfUnmodifiedSince: aws.Time(past),
	}); err != nil {
		t.Fatalf("CopyObject (if-match overrides if-unmodified-since): %v", err)
	}

	if got := getBody(t, client, "ovr", "dst"); got != "data" {
		t.Fatalf("override copy body = %q, want data", got)
	}
}

// TestSDKUploadPartCopyConditional verifies UploadPartCopy honours the same
// copy-source preconditions as CopyObject: a mismatched if-match fails the part
// copy with 412 PreconditionFailed.
func TestSDKUploadPartCopyConditional(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "upcc")
	putObject(t, client, "upcc", "src", "COPIED-PART-DATA", "", nil)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String("upcc"), Key: aws.String("dst"),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	uploadID := aws.ToString(created.UploadId)

	_, err = client.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
		Bucket: aws.String("upcc"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(1), CopySource: aws.String("upcc/src"),
		CopySourceIfMatch: aws.String("\"0000000000000000000000000000dead\""),
	})
	assertAPICode(t, err, "PreconditionFailed")
	assertStatus(t, err, 412)

	// A matching if-match lets the part copy proceed.
	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("upcc"), Key: aws.String("src")})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	if _, err := client.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
		Bucket: aws.String("upcc"), Key: aws.String("dst"), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(1), CopySource: aws.String("upcc/src"),
		CopySourceIfMatch: head.ETag,
	}); err != nil {
		t.Fatalf("UploadPartCopy (matching if-match): %v", err)
	}
}

// TestSDKCopyObjectSelfCopy verifies self-copy with the default COPY directive
// is rejected (InvalidRequest), while REPLACE makes it legal.
func TestSDKCopyObjectSelfCopy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "self")
	putObject(t, client, "self", "k", "data", "", nil)

	_, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("self"), Key: aws.String("k"), CopySource: aws.String("self/k"),
	})
	assertAPICode(t, err, "InvalidRequest")
	assertStatus(t, err, 400)

	// The same self-copy is legal when it changes metadata (REPLACE).
	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("self"), Key: aws.String("k"), CopySource: aws.String("self/k"),
		MetadataDirective: types.MetadataDirectiveReplace,
		Metadata:          map[string]string{"changed": "yes"},
	}); err != nil {
		t.Fatalf("self-copy with REPLACE: %v", err)
	}
}

// TestSDKCopyObjectRecomputesMultipartSourceETag verifies real S3 semantics:
// CopyObject's destination is always a fresh single-PUT object, so its ETag is
// the plain 32-hex-char MD5 of the copied bytes — even when the source was
// uploaded via multipart and therefore carries a "...-N" ETag. Without the
// fix, cloudemu propagated the source's multipart ETag onto the destination,
// which is observable to any tool comparing ETags after a copy.
func TestSDKCopyObjectRecomputesMultipartSourceETag(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "mp-copy-etag"
	const srcKey = "src"
	const dstKey = "dst"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(srcKey),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	uploadID := aws.ToString(created.UploadId)

	part1 := bytes.Repeat([]byte("A"), 8)
	part2 := bytes.Repeat([]byte("B"), 8)
	full := append(append([]byte{}, part1...), part2...)

	up1, err := client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(srcKey), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(1), Body: bytes.NewReader(part1),
	})
	if err != nil {
		t.Fatalf("UploadPart 1: %v", err)
	}

	up2, err := client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(srcKey), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(2), Body: bytes.NewReader(part2),
	})
	if err != nil {
		t.Fatalf("UploadPart 2: %v", err)
	}

	completed, err := client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(srcKey), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{
			{ETag: up1.ETag, PartNumber: aws.Int32(1)},
			{ETag: up2.ETag, PartNumber: aws.Int32(2)},
		}},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	srcETag := strings.Trim(aws.ToString(completed.ETag), `"`)
	if !strings.HasSuffix(srcETag, "-2") {
		t.Fatalf("source ETag = %q, want a multipart ETag ending in -2", srcETag)
	}

	sum := md5.Sum(full) //nolint:gosec // S3 ETag is MD5 by spec, not a security control
	wantDstETag := hex.EncodeToString(sum[:])

	copyOut, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(dstKey),
		CopySource: aws.String(bucket + "/" + srcKey),
	})
	if err != nil {
		t.Fatalf("CopyObject: %v", err)
	}

	gotDstETag := strings.Trim(aws.ToString(copyOut.CopyObjectResult.ETag), `"`)
	if gotDstETag != wantDstETag {
		t.Fatalf("CopyObject dst ETag = %q, want plain MD5 %q (source multipart ETag was %q)",
			gotDstETag, wantDstETag, srcETag)
	}

	if strings.Contains(gotDstETag, "-") {
		t.Fatalf("CopyObject dst ETag = %q still carries a multipart suffix", gotDstETag)
	}

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(dstKey)})
	if err != nil {
		t.Fatalf("HeadObject dst: %v", err)
	}

	if got := strings.Trim(aws.ToString(head.ETag), `"`); got != wantDstETag {
		t.Fatalf("HeadObject dst ETag = %q, want %q", got, wantDstETag)
	}

	// A copy of a normally-uploaded (non-multipart) object is unchanged: its
	// ETag was already a plain MD5, so recomputing it yields the same value.
	putObject(t, client, bucket, "plain-src", "hello world", "", nil)

	plainHead, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("plain-src")})
	if err != nil {
		t.Fatalf("HeadObject plain-src: %v", err)
	}

	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("plain-dst"),
		CopySource: aws.String(bucket + "/plain-src"),
	}); err != nil {
		t.Fatalf("CopyObject plain: %v", err)
	}

	plainDstHead, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("plain-dst")})
	if err != nil {
		t.Fatalf("HeadObject plain-dst: %v", err)
	}

	if aws.ToString(plainDstHead.ETag) != aws.ToString(plainHead.ETag) {
		t.Fatalf("plain copy dst ETag = %q, want unchanged source ETag %q",
			aws.ToString(plainDstHead.ETag), aws.ToString(plainHead.ETag))
	}
}

func assertAPICode(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %q, got nil", want)
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v, want smithy.APIError", err, err)
	}

	if apiErr.ErrorCode() != want {
		t.Fatalf("error code = %q, want %q", apiErr.ErrorCode(), want)
	}
}

func assertStatus(t *testing.T, err error, want int) {
	t.Helper()

	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("error = %T %v, want *awshttp.ResponseError", err, err)
	}

	if re.HTTPStatusCode() != want {
		t.Fatalf("HTTP status = %d, want %d", re.HTTPStatusCode(), want)
	}
}
