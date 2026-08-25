// delete_notification_sdk_test.go — real aws-sdk-go-v2 tests proving that
// DeleteObject and the batch DeleteObjects (POST ?delete) endpoints fire
// s3:ObjectRemoved notifications end-to-end (PutObject -> ObjectCreated already
// worked; delete never delivered), and that the delivered eventName picks the
// documented ObjectRemoved:Delete vs ObjectRemoved:DeleteMarkerCreated variant.
// Also covers CompleteMultipartUpload returning x-amz-version-id on a
// versioning-Enabled bucket.
package s3_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newS3SQSSuite stands up S3 and SQS on the same emulator (wired to each other
// via the provider factory's cross-service injectors), and returns ready SDK
// clients for both.
func newS3SQSSuite(t *testing.T) (*awss3.Client, *sqs.Client) {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{S3: provider.S3, SQS: provider.SQS})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)
	cfg.BaseEndpoint = aws.String(ts.URL)

	s3Client := awss3.NewFromConfig(cfg, func(o *awss3.Options) { o.UsePathStyle = true })
	sqsClient := sqs.NewFromConfig(cfg)

	return s3Client, sqsClient
}

// subscribeQueueToBucket creates an SQS queue, wires it as the bucket's
// ObjectRemoved notification target, and returns the queue URL to poll.
func subscribeQueueToBucket(t *testing.T, s3Client *awss3.Client, sqsClient *sqs.Client, bucket, queueName string) string {
	t.Helper()
	ctx := context.Background()

	queue, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(queueName)})
	require.NoError(t, err)

	attrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       queue.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	require.NoError(t, err)
	queueARN := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	require.NotEmpty(t, queueARN)

	_, err = s3Client.PutBucketNotificationConfiguration(ctx, &awss3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucket),
		NotificationConfiguration: &types.NotificationConfiguration{
			QueueConfigurations: []types.QueueConfiguration{{
				Id:       aws.String("removed"),
				QueueArn: aws.String(queueARN),
				Events:   []types.Event{types.EventS3ObjectRemoved},
			}},
		},
	})
	require.NoError(t, err)

	return aws.ToString(queue.QueueUrl)
}

// TestSDKDeleteObjectFiresObjectRemovedDelete proves DeleteObject on an
// unversioned bucket delivers s3:ObjectRemoved:Delete to a subscribed SQS
// queue — previously the wire delete path never fired notifications.
func TestSDKDeleteObjectFiresObjectRemovedDelete(t *testing.T) {
	s3Client, sqsClient := newS3SQSSuite(t)
	ctx := context.Background()

	const bucket = "del-notify-bucket"
	mustCreateBucket(t, s3Client, bucket)

	queueURL := subscribeQueueToBucket(t, s3Client, sqsClient, bucket, "removed-delete-queue")

	_, err := s3Client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("k"), Body: bytes.NewReader([]byte("v1")),
	})
	require.NoError(t, err)

	_, err = s3Client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("k")})
	require.NoError(t, err)

	msgs, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 1, "DeleteObject should deliver one ObjectRemoved notification")
	require.Contains(t, aws.ToString(msgs.Messages[0].Body), `"eventName":"ObjectRemoved:Delete"`)
}

// TestSDKDeleteObjectsBatchFiresObjectRemovedDelete proves the batch
// DeleteObjects (POST ?delete) endpoint also fires ObjectRemoved notifications,
// one per deleted key.
func TestSDKDeleteObjectsBatchFiresObjectRemovedDelete(t *testing.T) {
	s3Client, sqsClient := newS3SQSSuite(t)
	ctx := context.Background()

	const bucket = "del-batch-notify-bucket"
	mustCreateBucket(t, s3Client, bucket)

	queueURL := subscribeQueueToBucket(t, s3Client, sqsClient, bucket, "removed-batch-queue")

	for _, key := range []string{"a", "b"} {
		_, err := s3Client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("v")),
		})
		require.NoError(t, err)
	}

	_, err := s3Client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{{Key: aws.String("a")}, {Key: aws.String("b")}},
		},
	})
	require.NoError(t, err)

	msgs, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 2, "batch DeleteObjects should deliver one ObjectRemoved notification per key")

	for _, msg := range msgs.Messages {
		require.Contains(t, aws.ToString(msg.Body), `"eventName":"ObjectRemoved:Delete"`)
	}
}

// TestSDKDeleteObjectVersionedFiresDeleteMarkerCreated proves a top-level
// delete (no versionId) on a versioning-Enabled bucket fires
// ObjectRemoved:DeleteMarkerCreated (a marker is created, no bytes are
// permanently removed) rather than ObjectRemoved:Delete.
func TestSDKDeleteObjectVersionedFiresDeleteMarkerCreated(t *testing.T) {
	s3Client, sqsClient := newS3SQSSuite(t)
	ctx := context.Background()

	const bucket = "del-notify-versioned-bucket"
	mustCreateBucket(t, s3Client, bucket)

	_, err := s3Client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	})
	require.NoError(t, err)

	queueURL := subscribeQueueToBucket(t, s3Client, sqsClient, bucket, "removed-marker-queue")

	_, err = s3Client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("k"), Body: bytes.NewReader([]byte("v1")),
	})
	require.NoError(t, err)

	del, err := s3Client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("k")})
	require.NoError(t, err)
	require.True(t, aws.ToBool(del.DeleteMarker), "top-level delete on a versioned bucket should create a delete marker")

	msgs, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 1)
	require.Contains(t, aws.ToString(msgs.Messages[0].Body), `"eventName":"ObjectRemoved:DeleteMarkerCreated"`)

	// A subsequent permanent delete of that specific version fires
	// ObjectRemoved:Delete, not DeleteMarkerCreated, even though the removed
	// version is itself a delete marker.
	markerID := aws.ToString(del.VersionId)
	_, err = s3Client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("k"), VersionId: aws.String(markerID),
	})
	require.NoError(t, err)

	msgs, err = sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10,
	})
	require.NoError(t, err)
	require.Len(t, msgs.Messages, 1)
	require.Contains(t, aws.ToString(msgs.Messages[0].Body), `"eventName":"ObjectRemoved:Delete"`)
}

// TestSDKCompleteMultipartUploadReturnsVersionID proves CompleteMultipartUpload
// on a versioning-Enabled bucket reports x-amz-version-id (surfaced by the SDK
// as VersionId), matching the real internal version already assigned to the
// assembled object, and that it agrees with ListObjectVersions.
func TestSDKCompleteMultipartUploadReturnsVersionID(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "mp-version-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	_, err := client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	})
	require.NoError(t, err)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	uploadID := aws.ToString(created.UploadId)

	part, err := client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(1), Body: bytes.NewReader(bytes.Repeat([]byte("A"), 16)),
	})
	require.NoError(t, err)

	completed, err := client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.ETag}},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, aws.ToString(completed.VersionId), "CompleteMultipartUpload on a versioned bucket must return a version id")

	versions, err := client.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	require.Len(t, versions.Versions, 1)
	require.Equal(t, aws.ToString(completed.VersionId), aws.ToString(versions.Versions[0].VersionId))
}
