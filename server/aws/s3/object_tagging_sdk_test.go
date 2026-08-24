// object_tagging_sdk_test.go — real aws-sdk-go-v2 tests for the create-time
// x-amz-tagging header on PutObject, CopyObject, and CreateMultipartUpload. The
// tag-set supplied at write time must be retrievable via GetObjectTagging.
package s3_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tagMap flattens a GetObjectTagging TagSet into a comparable map.
func tagMap(set []types.Tag) map[string]string {
	out := make(map[string]string, len(set))
	for _, t := range set {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return out
}

func getObjectTags(t *testing.T, client *s3.Client, bucket, key string) map[string]string {
	t.Helper()

	out, err := client.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)

	return tagMap(out.TagSet)
}

// TestS3PutObjectTaggingHeader verifies the x-amz-tagging request header on
// PutObject sets the object's tag-set at upload time (previously silently
// dropped, leaving GetObjectTagging empty).
func TestS3PutObjectTaggingHeader(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "tagging-put"
	suiteCreateBucket(t, client, bucket)

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String("obj.txt"),
		Body:    bytes.NewReader([]byte("hi")),
		Tagging: aws.String("env=prod&team=payments"),
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"env": "prod", "team": "payments"},
		getObjectTags(t, client, bucket, "obj.txt"))
}

// TestS3CopyObjectTaggingReplace verifies x-amz-tagging-directive: REPLACE takes
// the destination tag-set from the request's x-amz-tagging header instead of the
// source object's tags.
func TestS3CopyObjectTaggingReplace(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "tagging-copy"
	suiteCreateBucket(t, client, bucket)

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String("src.txt"),
		Body:    bytes.NewReader([]byte("body")),
		Tagging: aws.String("origin=src"),
	})
	require.NoError(t, err)

	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:           aws.String(bucket),
		Key:              aws.String("dst.txt"),
		CopySource:       aws.String(bucket + "/src.txt"),
		TaggingDirective: types.TaggingDirectiveReplace,
		Tagging:          aws.String("origin=dst&stage=copied"),
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"origin": "dst", "stage": "copied"},
		getObjectTags(t, client, bucket, "dst.txt"), "REPLACE takes the request tag-set")
}

// TestS3CreateMultipartUploadTaggingHeader verifies the x-amz-tagging header on
// CreateMultipartUpload carries through to the completed object's tag-set.
func TestS3CreateMultipartUploadTaggingHeader(t *testing.T) {
	client := newSuiteS3Client(t)
	ctx := context.Background()

	const bucket = "tagging-mpu"
	suiteCreateBucket(t, client, bucket)

	create, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String("big.bin"),
		Tagging: aws.String("kind=archive"),
	})
	require.NoError(t, err)

	part := make([]byte, 5<<20) // one 5 MiB part
	up, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String("big.bin"),
		UploadId: create.UploadId, PartNumber: aws.Int32(1), Body: bytes.NewReader(part),
	})
	require.NoError(t, err)

	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String("big.bin"), UploadId: create.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{{ETag: up.ETag, PartNumber: aws.Int32(1)}},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"kind": "archive"},
		getObjectTags(t, client, bucket, "big.bin"))
}
