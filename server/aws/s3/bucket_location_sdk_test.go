package s3_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// TestSDKGetBucketLocation verifies GetBucketLocation reports the region the
// bucket was created in via CreateBucketConfiguration.LocationConstraint, and
// the empty constraint for a us-east-1 bucket.
func TestSDKGetBucketLocation(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// A bucket created with an explicit LocationConstraint reports that region.
	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String("west-bucket"),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraintUsWest2,
		},
	}); err != nil {
		t.Fatalf("CreateBucket (us-west-2): %v", err)
	}

	west, err := client.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("west-bucket")})
	if err != nil {
		t.Fatalf("GetBucketLocation (west): %v", err)
	}

	if west.LocationConstraint != types.BucketLocationConstraintUsWest2 {
		t.Fatalf("LocationConstraint = %q, want us-west-2", west.LocationConstraint)
	}

	// A default (us-east-1) bucket reports the empty constraint.
	mustCreateBucket(t, client, "east-bucket")

	east, err := client.GetBucketLocation(ctx, &awss3.GetBucketLocationInput{Bucket: aws.String("east-bucket")})
	if err != nil {
		t.Fatalf("GetBucketLocation (east): %v", err)
	}

	if east.LocationConstraint != "" {
		t.Fatalf("LocationConstraint = %q, want empty (us-east-1)", east.LocationConstraint)
	}
}
