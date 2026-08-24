package s3_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func assertS3NoLeakedPrefix(t *testing.T, err error) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an API error, got %v", err)
	}

	msg := apiErr.ErrorMessage()
	for _, p := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:"} {
		if strings.HasPrefix(msg, p) {
			t.Errorf("wire message %q leaks internal error-code prefix %q", msg, p)
		}
	}
}

// TestSDKS3ErrorMessagesHaveNoInternalPrefix pins that S3 wire error messages
// carry only the human sentence — not the internal cerrors code prefix — across
// NoSuchKey (missing object) and BucketNotEmpty (delete non-empty bucket).
func TestSDKS3ErrorMessagesHaveNoInternalPrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "shape-bucket"
	mustCreateBucket(t, client, bucket)

	// NoSuchKey: GetObject on a missing key.
	_, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("missing"),
	})
	if err == nil {
		t.Fatal("GetObject on a missing key: want error, got nil")
	}

	if got := s3Code(t, err); got != "NoSuchKey" {
		t.Errorf("GetObject code = %q, want NoSuchKey", got)
	}

	assertS3NoLeakedPrefix(t, err)

	// BucketNotEmpty: DeleteBucket on a bucket that still holds an object.
	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("k"),
		Body:   strings.NewReader("v"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	_, err = client.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		t.Fatal("DeleteBucket on a non-empty bucket: want error, got nil")
	}

	if got := s3Code(t, err); got != "BucketNotEmpty" {
		t.Errorf("DeleteBucket code = %q, want BucketNotEmpty", got)
	}

	assertS3NoLeakedPrefix(t, err)
}

func s3Code(t *testing.T, err error) string {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected an API error, got %v", err)
	}

	return apiErr.ErrorCode()
}
