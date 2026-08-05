package s3_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newSDKClient(t *testing.T) *awss3.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{S3: cloud.S3})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
}

func mustCreateBucket(t *testing.T, client *awss3.Client, bucket string) {
	t.Helper()

	_, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
}

// TestSDKMultipartUpload drives the real multipart flow (Create -> UploadPart x2
// -> Complete) and asserts a subsequent GetObject returns the concatenated body.
func TestSDKMultipartUpload(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "mp-bucket"
	const key = "big-object"

	mustCreateBucket(t, client, bucket)

	// Two parts. S3 requires all but the last part to be >= 5 MiB; the emulator
	// does not enforce that, so smaller distinct payloads suffice to verify
	// ordered concatenation.
	part1 := bytes.Repeat([]byte("A"), 1024)
	part2 := bytes.Repeat([]byte("B"), 2048)

	created, err := client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}

	uploadID := aws.ToString(created.UploadId)
	if uploadID == "" {
		t.Fatal("CreateMultipartUpload returned empty UploadId")
	}

	completed := make([]types.CompletedPart, 0, 2)

	for i, data := range [][]byte{part1, part2} {
		partNum := int32(i + 1) //nolint:gosec // small loop index

		up, upErr := client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(partNum),
			Body:       bytes.NewReader(data),
		})
		if upErr != nil {
			t.Fatalf("UploadPart %d: %v", partNum, upErr)
		}

		if aws.ToString(up.ETag) == "" {
			t.Fatalf("UploadPart %d returned empty ETag", partNum)
		}

		completed = append(completed, types.CompletedPart{
			ETag:       up.ETag,
			PartNumber: aws.Int32(partNum),
		})
	}

	_, err = client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	got, err := client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer got.Body.Close()

	body, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	want := append(append([]byte{}, part1...), part2...)
	if !bytes.Equal(body, want) {
		t.Fatalf("multipart body mismatch: got %d bytes, want %d bytes", len(body), len(want))
	}
}

// TestSDKMultipartAbortAndList verifies AbortMultipartUpload removes an
// in-progress upload from ListMultipartUploads.
func TestSDKMultipartAbortAndList(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "mp-abort-bucket"
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

	listed, err := client.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("ListMultipartUploads: %v", err)
	}

	if len(listed.Uploads) != 1 || aws.ToString(listed.Uploads[0].UploadId) != uploadID {
		t.Fatalf("expected 1 in-progress upload %q, got %+v", uploadID, listed.Uploads)
	}

	_, err = client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		t.Fatalf("AbortMultipartUpload: %v", err)
	}

	after, err := client.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("ListMultipartUploads (after abort): %v", err)
	}

	if len(after.Uploads) != 0 {
		t.Fatalf("expected 0 uploads after abort, got %d", len(after.Uploads))
	}
}

// TestSDKObjectTagging verifies PutObjectTagging -> GetObjectTagging round-trips
// the tag set, and DeleteObjectTagging clears it.
func TestSDKObjectTagging(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "tag-bucket"
	const key = "tagged-object"

	mustCreateBucket(t, client, bucket)

	_, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte("hello")),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	_, err = client.PutObjectTagging(ctx, &awss3.PutObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Tagging: &types.Tagging{
			TagSet: []types.Tag{
				{Key: aws.String("env"), Value: aws.String("prod")},
				{Key: aws.String("team"), Value: aws.String("platform")},
			},
		},
	})
	if err != nil {
		t.Fatalf("PutObjectTagging: %v", err)
	}

	got, err := client.GetObjectTagging(ctx, &awss3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObjectTagging: %v", err)
	}

	tags := map[string]string{}
	for _, tag := range got.TagSet {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if tags["env"] != "prod" || tags["team"] != "platform" {
		t.Fatalf("tag round-trip mismatch: %+v", tags)
	}

	_, err = client.DeleteObjectTagging(ctx, &awss3.DeleteObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("DeleteObjectTagging: %v", err)
	}

	afterDel, err := client.GetObjectTagging(ctx, &awss3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObjectTagging (after delete): %v", err)
	}

	if len(afterDel.TagSet) != 0 {
		t.Fatalf("expected empty tag set after delete, got %+v", afterDel.TagSet)
	}
}

// TestSDKHeadBucketAndTagging is a regression guard for issue #319: HeadBucket
// (HEAD /{bucket}) returned 405, and PutBucketTagging (PUT /{bucket}?tagging)
// mis-routed to CreateBucket and failed with BucketAlreadyOwnedByYou.
func TestSDKHeadBucketAndTagging(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "hb-bucket"

	mustCreateBucket(t, client, bucket)

	// HeadBucket on an existing bucket succeeds.
	if _, err := client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("HeadBucket(existing): %v", err)
	}

	// HeadBucket on a missing bucket is an error (404).
	if _, err := client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String("ghost")}); err == nil {
		t.Fatal("HeadBucket(missing): expected error, got nil")
	}

	// PutBucketTagging round-trips through the ?tagging sub-resource.
	if _, err := client.PutBucketTagging(ctx, &awss3.PutBucketTaggingInput{
		Bucket: aws.String(bucket),
		Tagging: &types.Tagging{TagSet: []types.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
		}},
	}); err != nil {
		t.Fatalf("PutBucketTagging: %v", err)
	}

	got, err := client.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("GetBucketTagging: %v", err)
	}

	if len(got.TagSet) != 1 || aws.ToString(got.TagSet[0].Key) != "env" {
		t.Fatalf("GetBucketTagging = %+v", got.TagSet)
	}

	if _, err := client.DeleteBucketTagging(ctx, &awss3.DeleteBucketTaggingInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("DeleteBucketTagging: %v", err)
	}
}

// TestSDKListObjectsV2MaxKeys is a regression guard for issue #319:
// ListObjectsV2 ignored MaxKeys, returned every key with IsTruncated=false and
// no continuation token, breaking any client that pages large buckets.
func TestSDKListObjectsV2MaxKeys(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "paged-bucket"

	mustCreateBucket(t, client, bucket)

	for _, k := range []string{"k1", "k2", "k3", "k4", "k5"} {
		if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(k), Body: bytes.NewReader([]byte("x")),
		}); err != nil {
			t.Fatalf("PutObject %s: %v", k, err)
		}
	}

	first, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket), MaxKeys: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListObjectsV2 page 1: %v", err)
	}

	if len(first.Contents) != 2 || !aws.ToBool(first.IsTruncated) || aws.ToString(first.NextContinuationToken) == "" {
		t.Fatalf("page 1: got %d keys, truncated=%v, token=%q",
			len(first.Contents), aws.ToBool(first.IsTruncated), aws.ToString(first.NextContinuationToken))
	}

	second, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String(bucket), MaxKeys: aws.Int32(2),
		ContinuationToken: first.NextContinuationToken,
	})
	if err != nil {
		t.Fatalf("ListObjectsV2 page 2: %v", err)
	}

	if len(second.Contents) != 2 {
		t.Fatalf("page 2: got %d keys, want 2", len(second.Contents))
	}

	if aws.ToString(first.Contents[0].Key) == aws.ToString(second.Contents[0].Key) {
		t.Fatal("page 2 returned the same first key as page 1 — pagination not advancing")
	}
}

// TestSDKBucketVersioning verifies PutBucketVersioning(Enabled) ->
// GetBucketVersioning returns Enabled.
func TestSDKBucketVersioning(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "versioned-bucket"

	mustCreateBucket(t, client, bucket)

	// Fresh bucket: versioning not yet configured -> empty status.
	initial, err := client.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("GetBucketVersioning (initial): %v", err)
	}

	if initial.Status != "" {
		t.Fatalf("expected empty versioning status initially, got %q", initial.Status)
	}

	_, err = client.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	})
	if err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}

	got, err := client.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("GetBucketVersioning: %v", err)
	}

	if got.Status != types.BucketVersioningStatusEnabled {
		t.Fatalf("expected versioning Enabled, got %q", got.Status)
	}
}
