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

// TestListObjectsV1AlwaysOwner pins that ListObjects (v1) always returns an
// <Owner> element for each object — v1 has no fetch-owner parameter, so the
// element is unconditional, unlike ListObjectsV2.
func TestListObjectsV1AlwaysOwner(t *testing.T) {
	ctx := context.Background()
	client := newSDKClient(t)

	const bucket = "owner-bucket-v1"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("k1"),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	out, err := client.ListObjects(ctx, &awss3.ListObjectsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("Contents = %d, want 1", len(out.Contents))
	}
	if owner := out.Contents[0].Owner; owner == nil || aws.ToString(owner.ID) == "" {
		t.Fatalf("Contents[0].Owner = %+v, want a populated owner for v1", out.Contents[0].Owner)
	}
}
