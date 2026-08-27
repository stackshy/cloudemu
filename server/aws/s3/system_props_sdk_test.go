package s3_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// TestSDKSystemMetadataRoundTrip (B1): PutObject with the S3 system-defined
// object properties round-trips them on HeadObject, and a default CopyObject
// inherits Cache-Control from the source.
func TestSDKSystemMetadataRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "sysprops-bucket"
	const key = "doc.txt"

	mustCreateBucket(t, client, bucket)

	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:             aws.String(bucket),
		Key:                aws.String(key),
		Body:               bytes.NewReader([]byte("hello world")),
		CacheControl:       aws.String("max-age=3600"),
		ContentEncoding:    aws.String("gzip"),
		ContentDisposition: aws.String("attachment; filename=\"doc.txt\""),
		ContentLanguage:    aws.String("en-US"),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	assertStr(t, "CacheControl", aws.ToString(head.CacheControl), "max-age=3600")
	assertStr(t, "ContentEncoding", aws.ToString(head.ContentEncoding), "gzip")
	assertStr(t, "ContentDisposition", aws.ToString(head.ContentDisposition), "attachment; filename=\"doc.txt\"")
	assertStr(t, "ContentLanguage", aws.ToString(head.ContentLanguage), "en-US")

	const dstKey = "copy.txt"

	_, err = client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(dstKey), CopySource: aws.String(bucket + "/" + key),
	})
	if err != nil {
		t.Fatalf("CopyObject: %v", err)
	}

	copied, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(dstKey)})
	if err != nil {
		t.Fatalf("HeadObject(copy): %v", err)
	}

	assertStr(t, "copy CacheControl", aws.ToString(copied.CacheControl), "max-age=3600")
}

// TestSDKStorageClassRoundTrip (B2): a non-STANDARD storage class is persisted
// and reported on HeadObject and ListObjectsV2; a STANDARD object omits the
// x-amz-storage-class header (the SDK reports an empty class).
func TestSDKStorageClassRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "storageclass-bucket"

	mustCreateBucket(t, client, bucket)

	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("cold.txt"),
		Body: bytes.NewReader([]byte("cold")), StorageClass: types.StorageClassStandardIa,
	})
	if err != nil {
		t.Fatalf("PutObject(IA): %v", err)
	}

	_, err = client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("warm.txt"), Body: bytes.NewReader([]byte("warm")),
	})
	if err != nil {
		t.Fatalf("PutObject(STANDARD): %v", err)
	}

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("cold.txt")})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	assertStr(t, "HeadObject StorageClass", string(head.StorageClass), string(types.StorageClassStandardIa))

	warm, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("warm.txt")})
	if err != nil {
		t.Fatalf("HeadObject(warm): %v", err)
	}

	// Real S3 omits x-amz-storage-class for a STANDARD object, so the SDK reports
	// an empty class.
	assertStr(t, "STANDARD StorageClass omitted", string(warm.StorageClass), "")

	assertListStorageClass(t, client, bucket)
}

// assertListStorageClass verifies ListObjectsV2 reports the recorded class per key.
func assertListStorageClass(t *testing.T, client *awss3.Client, bucket string) {
	t.Helper()

	listed, err := client.ListObjectsV2(context.Background(), &awss3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}

	classes := make(map[string]string, len(listed.Contents))
	for _, o := range listed.Contents {
		classes[aws.ToString(o.Key)] = string(o.StorageClass)
	}

	assertStr(t, "list cold.txt class", classes["cold.txt"], string(types.StorageClassStandardIa))
	assertStr(t, "list warm.txt class", classes["warm.txt"], "STANDARD")
}

// TestSDKGetObjectAttributes (B3): GetObjectAttributes populates ETag, ObjectSize,
// and StorageClass; a missing key answers NoSuchKey.
func TestSDKGetObjectAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "attrs-bucket"
	const key = "obj.bin"

	mustCreateBucket(t, client, bucket)

	body := bytes.Repeat([]byte("x"), 512)

	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		Body: bytes.NewReader(body), StorageClass: types.StorageClassStandardIa,
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	attrs, err := client.GetObjectAttributes(ctx, &awss3.GetObjectAttributesInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		ObjectAttributes: []types.ObjectAttributes{
			types.ObjectAttributesEtag, types.ObjectAttributesObjectSize, types.ObjectAttributesStorageClass,
		},
	})
	if err != nil {
		t.Fatalf("GetObjectAttributes: %v", err)
	}

	if aws.ToString(attrs.ETag) == "" {
		t.Error("GetObjectAttributes: empty ETag")
	}

	if got := aws.ToInt64(attrs.ObjectSize); got != int64(len(body)) {
		t.Errorf("GetObjectAttributes ObjectSize = %d, want %d", got, len(body))
	}

	assertStr(t, "attrs StorageClass", string(attrs.StorageClass), string(types.StorageClassStandardIa))

	_, err = client.GetObjectAttributes(ctx, &awss3.GetObjectAttributesInput{
		Bucket: aws.String(bucket), Key: aws.String("missing.bin"),
		ObjectAttributes: []types.ObjectAttributes{types.ObjectAttributesEtag},
	})

	var nsk *types.NoSuchKey
	if !errors.As(err, &nsk) {
		t.Errorf("GetObjectAttributes(missing) error = %v, want NoSuchKey", err)
	}
}

// TestSDKHeadBucketRegion (B4): HeadBucket reports the bucket's region.
func TestSDKHeadBucketRegion(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "region-bucket"

	mustCreateBucket(t, client, bucket)

	head, err := client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("HeadBucket: %v", err)
	}

	assertStr(t, "BucketRegion", aws.ToString(head.BucketRegion), "us-east-1")
}

// assertStr fails the test when got != want.
func assertStr(t *testing.T, name, got, want string) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}
