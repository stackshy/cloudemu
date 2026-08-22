package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSStorageCompat drives a full S3 object lifecycle through the real
// aws-sdk-go-v2 S3 client and records one compat result per portable storage
// operation exercised. Operation names match docs/coverage/coverage.json
// (e.g. the ListObjectsV2 SDK call maps to the "ListObjects" driver op).
func TestAWSStorageCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{S3: provider.S3})
	client := sess.S3Client()
	ctx := context.Background()

	const (
		svc    = "storage"
		bucket = "compat-bucket"
		dst    = "compat-bucket-copy"
		key    = "greeting.txt"
	)

	body := []byte("hello cloudemu")

	sess.Op(svc, "CreateBucket", func() error {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
		return err
	})

	sess.Op(svc, "ListBuckets", func() error {
		out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			return err
		}

		return requireBucket(out, bucket)
	})

	sess.Op(svc, "PutObject", func() error {
		_, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(body),
			ContentType: aws.String("text/plain"),
		})

		return err
	})

	sess.Op(svc, "GetObject", func() error {
		out, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return err
		}
		defer out.Body.Close()

		got, err := io.ReadAll(out.Body)
		if err != nil {
			return err
		}

		if !bytes.Equal(got, body) {
			return fmt.Errorf("body round-trip mismatch: got %q want %q", got, body)
		}

		return nil
	})

	sess.Op(svc, "HeadObject", func() error {
		_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})

		return err
	})

	sess.Op(svc, "ListObjects", func() error {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err != nil {
			return err
		}

		if len(out.Contents) != 1 {
			return fmt.Errorf("expected 1 object, got %d", len(out.Contents))
		}

		return nil
	})

	sess.Op(svc, "CopyObject", func() error {
		if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(dst)}); err != nil {
			return err
		}

		_, err := client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(dst),
			Key:        aws.String(key),
			CopySource: aws.String(bucket + "/" + key),
		})

		return err
	})

	sess.Op(svc, "DeleteObject", func() error {
		_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})

		return err
	})

	sess.Op(svc, "DeleteBucket", func() error {
		_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
		return err
	})
}

func requireBucket(out *s3.ListBucketsOutput, name string) error {
	for _, b := range out.Buckets {
		if aws.ToString(b.Name) == name {
			return nil
		}
	}

	return fmt.Errorf("bucket %q not found in ListBuckets", name)
}
