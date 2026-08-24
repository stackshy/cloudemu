package s3_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestListObjectsV2FetchOwner pins that ListObjectsV2 with FetchOwner=true
// includes an <Owner> element (ID + DisplayName) for each object, matching real
// S3; without the flag the element is omitted.
func TestListObjectsV2FetchOwner(t *testing.T) {
	ctx := context.Background()
	client := newSDKClient(t)

	const bucket = "owner-bucket"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("k1"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	// FetchOwner=true → Owner present and populated.
	withOwner, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket:     aws.String(bucket),
		FetchOwner: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("ListObjectsV2(FetchOwner=true): %v", err)
	}
	if len(withOwner.Contents) != 1 {
		t.Fatalf("Contents = %d, want 1", len(withOwner.Contents))
	}
	owner := withOwner.Contents[0].Owner
	if owner == nil || aws.ToString(owner.ID) == "" {
		t.Fatalf("Contents[0].Owner = %+v, want a populated owner", owner)
	}

	// FetchOwner unset → Owner omitted.
	noOwner, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("ListObjectsV2(default): %v", err)
	}
	if len(noOwner.Contents) != 1 {
		t.Fatalf("Contents = %d, want 1", len(noOwner.Contents))
	}
	if noOwner.Contents[0].Owner != nil {
		t.Fatalf("Contents[0].Owner = %+v, want nil without FetchOwner", noOwner.Contents[0].Owner)
	}
}
