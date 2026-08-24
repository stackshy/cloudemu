package s3_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// TestSDKGetDeleteMarkerVersion verifies that a version-addressed GET/HEAD of a
// delete marker returns 405 MethodNotAllowed with x-amz-delete-marker: true,
// not 404 — a delete marker has no retrievable content.
func TestSDKGetDeleteMarkerVersion(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "dmb")

	if _, err := client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String("dmb"),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("dmb"), Key: aws.String("k"), Body: bytes.NewReader([]byte("v1")),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	del, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("dmb"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	markerID := aws.ToString(del.VersionId)
	if markerID == "" || !aws.ToBool(del.DeleteMarker) {
		t.Fatalf("DeleteObject did not report a delete marker: id=%q marker=%v", markerID, aws.ToBool(del.DeleteMarker))
	}

	_, err = client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String("dmb"), Key: aws.String("k"), VersionId: aws.String(markerID),
	})

	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("GetObject(delete-marker version) error = %T %v, want *awshttp.ResponseError", err, err)
	}

	if re.HTTPStatusCode() != 405 {
		t.Fatalf("GetObject(delete-marker version) status = %d, want 405", re.HTTPStatusCode())
	}

	if re.Response.Header.Get("x-amz-delete-marker") != "true" {
		t.Fatalf("missing x-amz-delete-marker: true header; got %q", re.Response.Header.Get("x-amz-delete-marker"))
	}

	// HEAD of the same delete-marker version is likewise a 405.
	_, err = client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String("dmb"), Key: aws.String("k"), VersionId: aws.String(markerID),
	})

	if !errors.As(err, &re) || re.HTTPStatusCode() != 405 {
		t.Fatalf("HeadObject(delete-marker version) = %v, want 405", err)
	}
}
