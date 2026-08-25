package s3_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// putSimple seeds a key and returns its ETag.
func putSimple(t *testing.T, c *awss3.Client, bucket, key, body string) string {
	t.Helper()

	out, err := c.PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte(body)),
	})
	if err != nil {
		t.Fatalf("PutObject %s: %v", key, err)
	}

	return aws.ToString(out.ETag)
}

func mustGetBody(t *testing.T, c *awss3.Client, bucket, key string) string {
	t.Helper()

	out, err := c.GetObject(context.Background(), &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObject %s: %v", key, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(data)
}

// TestSDKPutObjectIfNoneMatchStar drives the real S3 conditional-write flow:
// create-if-absent succeeds on a new key and returns 412 PreconditionFailed on an
// existing key, without clobbering the stored object.
func TestSDKPutObjectIfNoneMatchStar(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-write", "obj"
	mustCreateBucket(t, client, bucket)

	// First create-if-absent succeeds.
	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte("v1")),
		IfNoneMatch: aws.String("*"),
	}); err != nil {
		t.Fatalf("create-if-absent (new key): %v", err)
	}

	// Second create-if-absent over the existing key -> 412, no overwrite.
	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte("v2")),
		IfNoneMatch: aws.String("*"),
	})
	assertAPICode(t, err, "PreconditionFailed")
	assertStatus(t, err, 412)

	if got := mustGetBody(t, client, bucket, key); got != "v1" {
		t.Fatalf("body = %q, want v1 (failed condition must not overwrite)", got)
	}
}

// TestSDKPutObjectIfMatch verifies optimistic replace: a matching ETag succeeds
// and a stale ETag returns 412 without overwriting.
func TestSDKPutObjectIfMatch(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-write-match", "obj"
	mustCreateBucket(t, client, bucket)
	etag := putSimple(t, client, bucket, key, "v1")

	// Matching ETag -> success.
	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		Body:    bytes.NewReader([]byte("v2")),
		IfMatch: aws.String(etag),
	}); err != nil {
		t.Fatalf("If-Match matching: %v", err)
	}

	// Stale ETag -> 412, no overwrite.
	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		Body:    bytes.NewReader([]byte("v3")),
		IfMatch: aws.String("\"0000000000000000000000000000dead\""),
	})
	assertAPICode(t, err, "PreconditionFailed")
	assertStatus(t, err, 412)

	if got := mustGetBody(t, client, bucket, key); got != "v2" {
		t.Fatalf("body = %q, want v2", got)
	}
}

// TestSDKGetObjectIfNoneMatch verifies a revalidating GET: a matching
// If-None-Match returns 304 Not Modified, and a non-matching one serves the body.
func TestSDKGetObjectIfNoneMatch(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-read", "obj"
	mustCreateBucket(t, client, bucket)
	etag := putSimple(t, client, bucket, key, "hello")

	// Matching ETag -> 304.
	_, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		IfNoneMatch: aws.String(etag),
	})
	assertStatus(t, err, 304)

	// Different ETag -> 200 + body.
	out, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		IfNoneMatch: aws.String("\"0000000000000000000000000000dead\""),
	})
	if err != nil {
		t.Fatalf("GetObject If-None-Match mismatch: %v", err)
	}
	out.Body.Close()
}

// TestSDKGetObjectIfMatch verifies a wrong If-Match returns 412.
func TestSDKGetObjectIfMatch(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-read-match", "obj"
	mustCreateBucket(t, client, bucket)
	etag := putSimple(t, client, bucket, key, "hello")

	// Matching ETag -> 200.
	out, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		IfMatch: aws.String(etag),
	})
	if err != nil {
		t.Fatalf("GetObject If-Match matching: %v", err)
	}
	out.Body.Close()

	// Wrong ETag -> 412.
	_, err = client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		IfMatch: aws.String("\"0000000000000000000000000000dead\""),
	})
	assertAPICode(t, err, "PreconditionFailed")
	assertStatus(t, err, 412)
}

// TestSDKGetObjectIfModifiedSince verifies a future If-Modified-Since returns 304
// (the object has not been modified since a future time).
func TestSDKGetObjectIfModifiedSince(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-read-time", "obj"
	mustCreateBucket(t, client, bucket)
	putSimple(t, client, bucket, key, "hello")

	_, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		IfModifiedSince: aws.Time(time.Now().Add(24 * time.Hour)),
	})
	assertStatus(t, err, 304)
}

// TestSDKGetObjectIfUnmodifiedSince verifies a past If-Unmodified-Since returns
// 412 (the object was modified after that time).
func TestSDKGetObjectIfUnmodifiedSince(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-read-unmod", "obj"
	mustCreateBucket(t, client, bucket)
	putSimple(t, client, bucket, key, "hello")

	_, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		IfUnmodifiedSince: aws.Time(time.Now().Add(-24 * time.Hour)),
	})
	assertAPICode(t, err, "PreconditionFailed")
	assertStatus(t, err, 412)
}

// TestSDKGetObjectIfMatchOverridesUnmodifiedSince pins RFC 7232 precedence: a
// true If-Match overrides a false If-Unmodified-Since, so S3 serves 200.
func TestSDKGetObjectIfMatchOverridesUnmodifiedSince(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-read-prec", "obj"
	mustCreateBucket(t, client, bucket)
	etag := putSimple(t, client, bucket, key, "hello")

	out, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		IfMatch:           aws.String(etag),
		IfUnmodifiedSince: aws.Time(time.Now().Add(-24 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("If-Match should override If-Unmodified-Since, got: %v", err)
	}
	out.Body.Close()
}

// TestSDKHeadObjectIfNoneMatch verifies conditional HEAD returns 304 on a match.
func TestSDKHeadObjectIfNoneMatch(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket, key = "cond-head", "obj"
	mustCreateBucket(t, client, bucket)
	etag := putSimple(t, client, bucket, key, "hello")

	_, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		IfNoneMatch: aws.String(etag),
	})
	assertStatus(t, err, 304)
}
