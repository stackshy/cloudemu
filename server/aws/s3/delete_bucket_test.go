package s3_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// TestSDKDeleteBucketVersionedNotEmpty asserts DeleteBucket fails with
// BucketNotEmpty when a versioning-enabled bucket still retains noncurrent
// object versions and delete markers, even though no current object is visible.
// Real S3 requires every version (including delete markers) to be removed first.
func TestSDKDeleteBucketVersionedNotEmpty(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "ver-del-bucket"
	const key = "obj.txt"

	mustCreateBucket(t, client, bucket)

	if _, err := client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("payload")),
		}); err != nil {
			t.Fatalf("PutObject %d: %v", i, err)
		}
	}

	// Top-level delete records a delete marker; the current-object view is now
	// empty while the version history holds 2 versions + 1 marker.
	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	versions, err := client.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("ListObjectVersions: %v", err)
	}

	if len(versions.Versions) != 2 || len(versions.DeleteMarkers) != 1 {
		t.Fatalf("version history = %d versions, %d markers; want 2 and 1",
			len(versions.Versions), len(versions.DeleteMarkers))
	}

	_, err = client.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		t.Fatal("DeleteBucket succeeded on a bucket with retained versions; want BucketNotEmpty")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("DeleteBucket error = %T %v, want smithy.APIError", err, err)
	}

	if apiErr.ErrorCode() != "BucketNotEmpty" {
		t.Fatalf("DeleteBucket code = %q, want BucketNotEmpty", apiErr.ErrorCode())
	}
}
