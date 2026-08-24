package aws_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestS3ListObjectsV2StartAfterSDK proves ListObjectsV2 begins listing strictly
// after the StartAfter key (lexicographic) and echoes StartAfter in the
// response, matching real S3.
func TestS3ListObjectsV2StartAfterSDK(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("start-after-bucket"),
	})
	require.NoError(t, err)

	for _, key := range []string{"a", "b", "c", "d"} {
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String("start-after-bucket"),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		})
		require.NoError(t, err)
	}

	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:     aws.String("start-after-bucket"),
		StartAfter: aws.String("b"),
	})
	require.NoError(t, err)

	got := make([]string, 0, len(result.Contents))
	for i := range result.Contents {
		got = append(got, aws.ToString(result.Contents[i].Key))
	}

	assert.Equal(t, []string{"c", "d"}, got)
	assert.Equal(t, "b", aws.ToString(result.StartAfter), "StartAfter must be echoed in the response")
}
