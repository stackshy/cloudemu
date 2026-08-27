package s3_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// TestSDKCreateBucketInvalidName asserts CreateBucket rejects names that violate
// S3's general-purpose bucket naming rules with 400 InvalidBucketName, matching
// real S3. Without the wire-layer validation these all succeed with 200.
func TestSDKCreateBucketInvalidName(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		bucket string
	}{
		{"uppercase", "UPPERCASE"},
		{"too-short", "ab"},
		{"underscore", "under_score"},
		{"adjacent-dots", "bad..dots"},
		{"leading-hyphen", "-badname"},
		{"trailing-hyphen", "badname-"},
		{"ip-address", "192.168.5.4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{
				Bucket: aws.String(tc.bucket),
			})
			if err == nil {
				t.Fatalf("CreateBucket(%q) succeeded, want InvalidBucketName", tc.bucket)
			}

			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("CreateBucket(%q) error = %T %v, want smithy.APIError", tc.bucket, err, err)
			}

			if apiErr.ErrorCode() != "InvalidBucketName" {
				t.Fatalf("CreateBucket(%q) code = %q, want InvalidBucketName", tc.bucket, apiErr.ErrorCode())
			}
		})
	}
}

// TestSDKCreateBucketValidName asserts a conforming name still succeeds, guarding
// against the validation rejecting legal bucket names.
func TestSDKCreateBucketValidName(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, name := range []string{"abc", "my-bucket", "my.bucket.name", "a1b2c3"} {
		if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{
			Bucket: aws.String(name),
		}); err != nil {
			t.Fatalf("CreateBucket(%q) failed: %v", name, err)
		}
	}
}
