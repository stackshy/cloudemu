package s3_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newLockSDKClient builds an S3 SDK client backed by a FakeClock the test can
// advance, so retain-until-date expiry is deterministic.
func newLockSDKClient(t *testing.T) (*awss3.Client, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(config.WithClock(fc))
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

	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})

	return client, fc
}

// lockEpoch is the FakeClock's start; retain-until dates are expressed relative
// to it so advancing the clock crosses them deterministically.
func lockEpoch() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }

func mustCreateObjectLockBucket(t *testing.T, client *awss3.Client, bucket string) {
	t.Helper()

	_, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{
		Bucket:                     aws.String(bucket),
		ObjectLockEnabledForBucket: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateBucket(object-lock): %v", err)
	}
}

// assertAccessDenied asserts the SDK error is an S3 AccessDenied (403).
func assertAccessDenied(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected AccessDenied error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "AccessDenied" {
		t.Fatalf("expected AccessDenied, got %v", err)
	}
}

// TestSDKObjectLockComplianceBlocksDelete drives the real aws-sdk-go-v2 flow: a
// COMPLIANCE-retained version cannot be deleted until the retain date passes.
func TestSDKObjectLockComplianceBlocksDelete(t *testing.T) {
	client, fc := newLockSDKClient(t)
	ctx := context.Background()

	mustCreateObjectLockBucket(t, client, "compliance")

	until := lockEpoch().Add(time.Hour)
	put, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:                    aws.String("compliance"),
		Key:                       aws.String("k"),
		Body:                      bytes.NewReader([]byte("v1")),
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: aws.Time(until),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	vid := aws.ToString(put.VersionId)
	if vid == "" {
		t.Fatal("PutObject returned no version id")
	}

	// GetObjectRetention round-trips the mode.
	ret, err := client.GetObjectRetention(ctx, &awss3.GetObjectRetentionInput{
		Bucket: aws.String("compliance"), Key: aws.String("k"), VersionId: aws.String(vid),
	})
	if err != nil {
		t.Fatalf("GetObjectRetention: %v", err)
	}
	if ret.Retention == nil || ret.Retention.Mode != types.ObjectLockRetentionModeCompliance {
		t.Fatalf("GetObjectRetention mode = %v, want COMPLIANCE", ret.Retention)
	}

	// Deleting the retained version is refused, even with governance bypass.
	_, err = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("compliance"), Key: aws.String("k"), VersionId: aws.String(vid),
		BypassGovernanceRetention: aws.Bool(true),
	})
	assertAccessDenied(t, err)

	// After the retain date, the delete succeeds.
	fc.Advance(2 * time.Hour)

	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("compliance"), Key: aws.String("k"), VersionId: aws.String(vid),
	}); err != nil {
		t.Fatalf("DeleteObject after expiry: %v", err)
	}
}

// TestSDKObjectLockGovernanceBypass verifies GOVERNANCE retention blocks a delete
// but x-amz-bypass-governance-retention permits it.
func TestSDKObjectLockGovernanceBypass(t *testing.T) {
	client, _ := newLockSDKClient(t)
	ctx := context.Background()

	mustCreateObjectLockBucket(t, client, "governance")

	put, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:                    aws.String("governance"),
		Key:                       aws.String("k"),
		Body:                      bytes.NewReader([]byte("v1")),
		ObjectLockMode:            types.ObjectLockModeGovernance,
		ObjectLockRetainUntilDate: aws.Time(lockEpoch().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	vid := aws.ToString(put.VersionId)

	_, err = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("governance"), Key: aws.String("k"), VersionId: aws.String(vid),
	})
	assertAccessDenied(t, err)

	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("governance"), Key: aws.String("k"), VersionId: aws.String(vid),
		BypassGovernanceRetention: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteObject with bypass: %v", err)
	}
}

// TestSDKObjectLockLegalHold verifies legal hold blocks a delete regardless of
// retention, until it is turned OFF.
func TestSDKObjectLockLegalHold(t *testing.T) {
	client, _ := newLockSDKClient(t)
	ctx := context.Background()

	mustCreateObjectLockBucket(t, client, "legalhold")

	put, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:                    aws.String("legalhold"),
		Key:                       aws.String("k"),
		Body:                      bytes.NewReader([]byte("v1")),
		ObjectLockLegalHoldStatus: types.ObjectLockLegalHoldStatusOn,
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	vid := aws.ToString(put.VersionId)

	lh, err := client.GetObjectLegalHold(ctx, &awss3.GetObjectLegalHoldInput{
		Bucket: aws.String("legalhold"), Key: aws.String("k"), VersionId: aws.String(vid),
	})
	if err != nil {
		t.Fatalf("GetObjectLegalHold: %v", err)
	}
	if lh.LegalHold == nil || lh.LegalHold.Status != types.ObjectLockLegalHoldStatusOn {
		t.Fatalf("legal hold = %v, want ON", lh.LegalHold)
	}

	// Blocked even with bypass (bypass is only for governance retention).
	_, err = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("legalhold"), Key: aws.String("k"), VersionId: aws.String(vid),
		BypassGovernanceRetention: aws.Bool(true),
	})
	assertAccessDenied(t, err)

	if _, err := client.PutObjectLegalHold(ctx, &awss3.PutObjectLegalHoldInput{
		Bucket: aws.String("legalhold"), Key: aws.String("k"), VersionId: aws.String(vid),
		LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff},
	}); err != nil {
		t.Fatalf("PutObjectLegalHold OFF: %v", err)
	}

	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("legalhold"), Key: aws.String("k"), VersionId: aws.String(vid),
	}); err != nil {
		t.Fatalf("DeleteObject after legal hold off: %v", err)
	}
}

// TestSDKObjectLockTopLevelDeleteMarker verifies a top-level DeleteObject (no
// version id) on a locked object still records a delete marker, leaving the
// protected version intact.
func TestSDKObjectLockTopLevelDeleteMarker(t *testing.T) {
	client, _ := newLockSDKClient(t)
	ctx := context.Background()

	mustCreateObjectLockBucket(t, client, "marker")

	put, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:                    aws.String("marker"),
		Key:                       aws.String("k"),
		Body:                      bytes.NewReader([]byte("v1")),
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: aws.Time(lockEpoch().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	lockedVID := aws.ToString(put.VersionId)

	del, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("marker"), Key: aws.String("k"),
	})
	if err != nil {
		t.Fatalf("DeleteObject (top-level): %v", err)
	}
	if !aws.ToBool(del.DeleteMarker) {
		t.Fatalf("top-level delete did not create a delete marker")
	}

	// The locked version is still listed.
	vers, err := client.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("marker")})
	if err != nil {
		t.Fatalf("ListObjectVersions: %v", err)
	}
	found := false
	for _, v := range vers.Versions {
		if aws.ToString(v.VersionId) == lockedVID {
			found = true
		}
	}
	if !found {
		t.Fatal("locked version missing after top-level delete")
	}
}
