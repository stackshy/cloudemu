package s3_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// TestSDKListObjectVersionsPagination asserts ListObjectVersions honors max-keys:
// it caps the page, echoes the requested MaxKeys, sets IsTruncated with
// NextKeyMarker/NextVersionIdMarker, and round-trips those markers to return the
// rest without gaps or overlaps.
func TestSDKListObjectVersionsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "versions-page-bucket"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	keys := []string{"a", "b", "c", "d"}
	for _, k := range keys {
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(k),
			Body:   bytes.NewReader([]byte("v-" + k)),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", k, err)
		}
	}

	page1, err := client.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{
		Bucket:  aws.String(bucket),
		MaxKeys: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListObjectVersions page1: %v", err)
	}

	if got := len(page1.Versions); got != 2 {
		t.Fatalf("page1 versions = %d, want 2 (max-keys must cap the page)", got)
	}

	if aws.ToInt32(page1.MaxKeys) != 2 {
		t.Errorf("page1 MaxKeys echoed = %d, want 2", aws.ToInt32(page1.MaxKeys))
	}

	if !aws.ToBool(page1.IsTruncated) {
		t.Error("page1 IsTruncated = false, want true (2 of 4 versions returned)")
	}

	if aws.ToString(page1.NextKeyMarker) != "c" {
		t.Errorf("page1 NextKeyMarker = %q, want %q", aws.ToString(page1.NextKeyMarker), "c")
	}

	if aws.ToString(page1.NextVersionIdMarker) == "" {
		t.Error("page1 NextVersionIdMarker is empty, want the version id of the first key not returned")
	}

	if firstKey := aws.ToString(page1.Versions[0].Key); firstKey != "a" {
		t.Errorf("page1 first key = %q, want %q", firstKey, "a")
	}

	page2, err := client.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{
		Bucket:          aws.String(bucket),
		MaxKeys:         aws.Int32(2),
		KeyMarker:       page1.NextKeyMarker,
		VersionIdMarker: page1.NextVersionIdMarker,
	})
	if err != nil {
		t.Fatalf("ListObjectVersions page2: %v", err)
	}

	if aws.ToString(page2.KeyMarker) != "c" {
		t.Errorf("page2 KeyMarker echoed = %q, want %q", aws.ToString(page2.KeyMarker), "c")
	}

	if aws.ToBool(page2.IsTruncated) {
		t.Error("page2 IsTruncated = true, want false (remaining versions fit)")
	}

	var page2Keys []string
	for _, v := range page2.Versions {
		page2Keys = append(page2Keys, aws.ToString(v.Key))
	}

	if strings.Join(page2Keys, ",") != "c,d" {
		t.Errorf("page2 keys = %v, want [c d] (resume must not gap or overlap)", page2Keys)
	}
}

// TestSDKListPartsPagination asserts ListParts honors max-parts and
// part-number-marker: it caps the page, echoes MaxParts, sets IsTruncated with
// NextPartNumberMarker (the last part returned), and resumes strictly after the
// marker.
func TestSDKListPartsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "parts-page-bucket"
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

	for _, n := range []int32{1, 2, 3} {
		if _, err := client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(n),
			Body:       bytes.NewReader(bytes.Repeat([]byte("A"), 8)),
		}); err != nil {
			t.Fatalf("UploadPart %d: %v", n, err)
		}
	}

	page1, err := client.ListParts(ctx, &awss3.ListPartsInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MaxParts: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("ListParts page1: %v", err)
	}

	if got := len(page1.Parts); got != 1 {
		t.Fatalf("page1 parts = %d, want 1 (max-parts must cap the page)", got)
	}

	if aws.ToInt32(page1.MaxParts) != 1 {
		t.Errorf("page1 MaxParts echoed = %d, want 1", aws.ToInt32(page1.MaxParts))
	}

	if !aws.ToBool(page1.IsTruncated) {
		t.Error("page1 IsTruncated = false, want true (1 of 3 parts returned)")
	}

	if aws.ToString(page1.NextPartNumberMarker) != "1" {
		t.Errorf("page1 NextPartNumberMarker = %q, want %q", aws.ToString(page1.NextPartNumberMarker), "1")
	}

	if aws.ToInt32(page1.Parts[0].PartNumber) != 1 {
		t.Errorf("page1 first part = %d, want 1", aws.ToInt32(page1.Parts[0].PartNumber))
	}

	page2, err := client.ListParts(ctx, &awss3.ListPartsInput{
		Bucket:           aws.String(bucket),
		Key:              aws.String(key),
		UploadId:         aws.String(uploadID),
		MaxParts:         aws.Int32(10),
		PartNumberMarker: page1.NextPartNumberMarker,
	})
	if err != nil {
		t.Fatalf("ListParts page2: %v", err)
	}

	if aws.ToString(page2.PartNumberMarker) != "1" {
		t.Errorf("page2 PartNumberMarker echoed = %q, want %q", aws.ToString(page2.PartNumberMarker), "1")
	}

	if aws.ToBool(page2.IsTruncated) {
		t.Error("page2 IsTruncated = true, want false (remaining parts fit)")
	}

	var nums []int32
	for _, p := range page2.Parts {
		nums = append(nums, aws.ToInt32(p.PartNumber))
	}

	if len(nums) != 2 || nums[0] != 2 || nums[1] != 3 {
		t.Errorf("page2 part numbers = %v, want [2 3] (resume strictly after marker)", nums)
	}
}

// TestSDKListMultipartUploadsPagination asserts ListMultipartUploads honors
// max-uploads and the key-marker/upload-id-marker pair: it caps the page, echoes
// MaxUploads, sets IsTruncated with NextKeyMarker/NextUploadIdMarker (the last
// upload returned), and resumes strictly after that pair.
func TestSDKListMultipartUploadsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "uploads-page-bucket"
	mustCreateBucket(t, client, bucket)

	keys := []string{"key-a", "key-b"}
	for _, k := range keys {
		if _, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(k),
		}); err != nil {
			t.Fatalf("CreateMultipartUpload %s: %v", k, err)
		}
	}

	page1, err := client.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{
		Bucket:     aws.String(bucket),
		MaxUploads: aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("ListMultipartUploads page1: %v", err)
	}

	if got := len(page1.Uploads); got != 1 {
		t.Fatalf("page1 uploads = %d, want 1 (max-uploads must cap the page)", got)
	}

	if aws.ToInt32(page1.MaxUploads) != 1 {
		t.Errorf("page1 MaxUploads echoed = %d, want 1", aws.ToInt32(page1.MaxUploads))
	}

	if !aws.ToBool(page1.IsTruncated) {
		t.Error("page1 IsTruncated = false, want true (1 of 2 uploads returned)")
	}

	if aws.ToString(page1.Uploads[0].Key) != "key-a" {
		t.Errorf("page1 first upload key = %q, want %q", aws.ToString(page1.Uploads[0].Key), "key-a")
	}

	if aws.ToString(page1.NextKeyMarker) != "key-a" {
		t.Errorf("page1 NextKeyMarker = %q, want %q (last upload returned)", aws.ToString(page1.NextKeyMarker), "key-a")
	}

	if aws.ToString(page1.NextUploadIdMarker) == "" {
		t.Error("page1 NextUploadIdMarker is empty, want the upload id of the last upload returned")
	}

	page2, err := client.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{
		Bucket:         aws.String(bucket),
		MaxUploads:     aws.Int32(10),
		KeyMarker:      page1.NextKeyMarker,
		UploadIdMarker: page1.NextUploadIdMarker,
	})
	if err != nil {
		t.Fatalf("ListMultipartUploads page2: %v", err)
	}

	if aws.ToString(page2.KeyMarker) != "key-a" {
		t.Errorf("page2 KeyMarker echoed = %q, want %q", aws.ToString(page2.KeyMarker), "key-a")
	}

	if aws.ToBool(page2.IsTruncated) {
		t.Error("page2 IsTruncated = true, want false (remaining uploads fit)")
	}

	if got := len(page2.Uploads); got != 1 || aws.ToString(page2.Uploads[0].Key) != "key-b" {
		t.Errorf("page2 uploads = %d keys, want exactly [key-b] (resume strictly after marker)", got)
	}
}
