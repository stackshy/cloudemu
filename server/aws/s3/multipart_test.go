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

// TestSDKCompleteMultipartWrongETag asserts CompleteMultipartUpload rejects a
// part whose supplied ETag does not match the uploaded part with 400
// InvalidPart, matching real S3. Without the ETag check the object completes
// silently with a corrupted-part reference.
func TestSDKCompleteMultipartWrongETag(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "mp-etag-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	uploadID := aws.ToString(created.UploadId)

	if _, err := client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(1),
		Body:       bytes.NewReader(bytes.Repeat([]byte("A"), 16)),
	}); err != nil {
		t.Fatalf("UploadPart: %v", err)
	}

	_, err = client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{{
				PartNumber: aws.Int32(1),
				ETag:       aws.String(`"00000000000000000000000000000000"`),
			}},
		},
	})
	if err == nil {
		t.Fatal("CompleteMultipartUpload accepted a wrong part ETag; want InvalidPart")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("CompleteMultipartUpload error = %T %v, want smithy.APIError", err, err)
	}

	if apiErr.ErrorCode() != "InvalidPart" {
		t.Fatalf("CompleteMultipartUpload code = %q, want InvalidPart", apiErr.ErrorCode())
	}
}
