package blobstore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/blobstore"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newS3Client boots the AWS wire server with the given StorageEngine wired in
// and returns a real service/s3 SDK client pointed at it.
func newS3Client(t *testing.T, eng config.StorageEngine) *awss3.Client {
	t.Helper()

	cloud := cloudemu.NewAWS(config.WithStorageEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
}

// getBody reads and returns the full object body for key.
func getBody(t *testing.T, client *awss3.Client, bucket, key string) []byte {
	t.Helper()

	out, err := client.GetObject(context.Background(), &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObject %q: %v", key, err)
	}

	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body %q: %v", key, err)
	}

	return body
}

// TestS3BlobstoreE2E runs the exact flow a real user runs against AWS S3 — the
// real service/s3 SDK against CloudEmu's wire server — but with object bytes
// persisted to a real local filesystem by blobstore. It then reads the backing
// file directly off disk to prove the bytes really landed there.
func TestS3BlobstoreE2E(t *testing.T) {
	eng := blobstore.New("")
	t.Cleanup(func() { _ = eng.Close() })

	client := newS3Client(t, eng)
	ctx := context.Background()

	const (
		bucket      = "user-uploads"
		key         = "reports/q3.txt"
		copyKey     = "reports/q3-copy.txt"
		contentType = "text/plain"
	)

	body := []byte("quarterly revenue up 12% — bytes on real disk\n")

	// 1. CreateBucket — exactly like `aws s3 mb`.
	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// 2. PutObject with a real body.
	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// 3. GetObject — bytes must round-trip through the engine.
	if got := getBody(t, client, bucket, key); !bytes.Equal(got, body) {
		t.Fatalf("GetObject body mismatch: got %q, want %q", got, body)
	}

	// 4. HeadObject / ListObjectsV2 — metadata (Size, ContentType) is served from
	// the in-memory Mock even though the bytes live in the engine.
	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	if aws.ToInt64(head.ContentLength) != int64(len(body)) {
		t.Fatalf("HeadObject size: got %d, want %d", aws.ToInt64(head.ContentLength), len(body))
	}

	if aws.ToString(head.ContentType) != contentType {
		t.Fatalf("HeadObject content-type: got %q, want %q", aws.ToString(head.ContentType), contentType)
	}

	listed, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}

	if len(listed.Contents) != 1 || aws.ToString(listed.Contents[0].Key) != key {
		t.Fatalf("ListObjectsV2 keys: %+v", listed.Contents)
	}

	if aws.ToInt64(listed.Contents[0].Size) != int64(len(body)) {
		t.Fatalf("ListObjectsV2 size: got %d, want %d", aws.ToInt64(listed.Contents[0].Size), len(body))
	}

	// 5. CopyObject then GetObject(copy) — server-side copy through the engine.
	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(copyKey),
		CopySource: aws.String(bucket + "/" + key),
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}

	if got := getBody(t, client, bucket, copyKey); !bytes.Equal(got, body) {
		t.Fatalf("GetObject(copy) body mismatch: got %q, want %q", got, body)
	}

	// 6. The proof: read the real backing file directly off disk and compare.
	onDisk := filepath.Join(eng.Root(), "buckets", bucket, "current", key)

	raw, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read backing file %s: %v", onDisk, err)
	}

	if !bytes.Equal(raw, body) {
		t.Fatalf("backing file bytes mismatch: got %q, want %q", raw, body)
	}

	// 7. DeleteObject then GetObject must 404 — and the backing file is gone.
	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	_, err = client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	var nsk *types.NoSuchKey
	if !errors.As(err, &nsk) {
		t.Fatalf("GetObject after delete: want NoSuchKey, got %v", err)
	}

	if _, statErr := os.Stat(onDisk); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("backing file still present after delete: %v", statErr)
	}
}
