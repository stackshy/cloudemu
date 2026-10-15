// object_tag_limits_sdk_test.go: real aws-sdk-go-v2 tests for the S3 object
// tag limits on PutObjectTagging and on the x-amz-tagging header of PutObject,
// CreateMultipartUpload and CopyObject.
package s3_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// sdkTagSet builds a TagSet of n distinct tags.
func sdkTagSet(n int) []types.Tag {
	set := make([]types.Tag, 0, n)
	for i := 0; i < n; i++ {
		set = append(set, types.Tag{Key: aws.String(fmt.Sprintf("k%d", i)), Value: aws.String("v")})
	}

	return set
}

// taggingHeader builds an x-amz-tagging value of n distinct tags.
func taggingHeader(n int) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("k%d=v", i))
	}

	return strings.Join(parts, "&")
}

func TestSDKPutObjectTaggingLimits(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "tags")

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("tags"), Key: aws.String("k"), Body: bytes.NewReader([]byte("x")),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	putTags := func(set []types.Tag) error {
		_, err := client.PutObjectTagging(ctx, &awss3.PutObjectTaggingInput{
			Bucket: aws.String("tags"), Key: aws.String("k"), Tagging: &types.Tagging{TagSet: set},
		})

		return err
	}

	requireAPIError(t, putTags(sdkTagSet(11)), 400, "BadRequest")
	requireAPIError(t, putTags([]types.Tag{{Key: aws.String(strings.Repeat("k", 129)), Value: aws.String("v")}}), 400, "InvalidTag")
	requireAPIError(t, putTags([]types.Tag{{Key: aws.String("k"), Value: aws.String(strings.Repeat("v", 257))}}), 400, "InvalidTag")
	requireAPIError(t, putTags([]types.Tag{
		{Key: aws.String("dup"), Value: aws.String("a")}, {Key: aws.String("dup"), Value: aws.String("b")},
	}), 400, "InvalidTag")
	requireAPIError(t, putTags([]types.Tag{{Key: aws.String("aws:x"), Value: aws.String("v")}}), 400, "InvalidTag")

	if err := putTags(sdkTagSet(10)); err != nil {
		t.Fatalf("PutObjectTagging with 10 tags: %v", err)
	}

	if got := getObjectTags(t, client, "tags", "k"); len(got) != 10 {
		t.Fatalf("tag count = %d, want 10", len(got))
	}
}

func TestSDKUploadTaggingHeaderLimits(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "hdrtags")

	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("hdrtags"), Key: aws.String("eleven"), Body: bytes.NewReader([]byte("x")),
		Tagging: aws.String(taggingHeader(11)),
	})
	requireAPIError(t, err, 400, "BadRequest")
	requireNoObject(t, client, "hdrtags", "eleven")

	_, err = client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("hdrtags"), Key: aws.String("dup"), Body: bytes.NewReader([]byte("x")),
		Tagging: aws.String("a=1&a=2"),
	})
	requireAPIError(t, err, 400, "InvalidArgument")
	requireNoObject(t, client, "hdrtags", "dup")

	_, err = client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String("hdrtags"), Key: aws.String("mp"), Tagging: aws.String(taggingHeader(11)),
	})
	requireAPIError(t, err, 400, "BadRequest")

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("hdrtags"), Key: aws.String("src"), Body: bytes.NewReader([]byte("x")),
	}); err != nil {
		t.Fatalf("PutObject src: %v", err)
	}

	_, err = client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("hdrtags"), Key: aws.String("dst"), CopySource: aws.String("hdrtags/src"),
		TaggingDirective: types.TaggingDirectiveReplace, Tagging: aws.String("k=" + strings.Repeat("v", 257)),
	})
	requireAPIError(t, err, 400, "InvalidTag")
	requireNoObject(t, client, "hdrtags", "dst")
}
