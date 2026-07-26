package s3_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestSDKListParts covers #266: ListParts now reports the parts buffered so
// far (ordered by part number) instead of an empty list, so resumable-upload
// tooling can reconstruct its state.
func TestSDKListParts(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "lp-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	uploadID := aws.ToString(created.UploadId)

	// Upload part 2 before part 1 to confirm ListParts sorts by part number.
	sizes := map[int32]int{1: 1024, 2: 2048}
	for _, pn := range []int32{2, 1} {
		if _, err := client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
			PartNumber: aws.Int32(pn), Body: bytes.NewReader(bytes.Repeat([]byte("x"), sizes[pn])),
		}); err != nil {
			t.Fatalf("UploadPart %d: %v", pn, err)
		}
	}

	out, err := client.ListParts(ctx, &awss3.ListPartsInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(out.Parts) != 2 {
		t.Fatalf("ListParts returned %d parts, want 2", len(out.Parts))
	}
	if aws.ToInt32(out.Parts[0].PartNumber) != 1 || aws.ToInt32(out.Parts[1].PartNumber) != 2 {
		t.Fatalf("parts not sorted ascending: got %d then %d",
			aws.ToInt32(out.Parts[0].PartNumber), aws.ToInt32(out.Parts[1].PartNumber))
	}
	if aws.ToInt64(out.Parts[0].Size) != 1024 || aws.ToInt64(out.Parts[1].Size) != 2048 {
		t.Fatalf("part sizes = %d, %d; want 1024, 2048",
			aws.ToInt64(out.Parts[0].Size), aws.ToInt64(out.Parts[1].Size))
	}
	for _, p := range out.Parts {
		if aws.ToString(p.ETag) == "" {
			t.Fatalf("part %d has empty ETag", aws.ToInt32(p.PartNumber))
		}
	}
}

// TestSDKUploadPartNumberOutOfRange covers #266: part numbers must be within
// 1..10000; anything larger is rejected with InvalidArgument.
func TestSDKUploadPartNumberOutOfRange(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "pn-bucket"
	const key = "obj"

	mustCreateBucket(t, client, bucket)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	uploadID := aws.ToString(created.UploadId)

	_, err = client.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(10001), Body: bytes.NewReader([]byte("x")),
	})
	if err == nil {
		t.Fatal("UploadPart with partNumber 10001 succeeded, want InvalidArgument")
	}
	if !strings.Contains(err.Error(), "InvalidArgument") {
		t.Fatalf("error = %v, want InvalidArgument", err)
	}
}
