// object_lock_config_sdk_test.go: real aws-sdk-go-v2 tests for
// Put/GetObjectLockConfiguration, bucket default retention, and the Object
// Lock headers on PutObject.
package s3_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func enableVersioning(t *testing.T, client *awss3.Client, bucket string) {
	t.Helper()

	if _, err := client.PutBucketVersioning(context.Background(), &awss3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("PutBucketVersioning: %v", err)
	}
}

func putLockConfig(client *awss3.Client, bucket string, rule *types.ObjectLockRule) error {
	_, err := client.PutObjectLockConfiguration(context.Background(), &awss3.PutObjectLockConfigurationInput{
		Bucket: aws.String(bucket),
		ObjectLockConfiguration: &types.ObjectLockConfiguration{
			ObjectLockEnabled: types.ObjectLockEnabledEnabled, Rule: rule,
		},
	})

	return err
}

func putBody(t *testing.T, client *awss3.Client, in *awss3.PutObjectInput) *awss3.PutObjectOutput {
	t.Helper()

	in.Body = bytes.NewReader([]byte("locked"))

	out, err := client.PutObject(context.Background(), in)
	if err != nil {
		t.Fatalf("PutObject %s: %v", aws.ToString(in.Key), err)
	}

	return out
}

// TestSDKPutObjectLockConfigurationExistingBucket enables Object Lock on an
// existing bucket and checks it takes effect: before the fix the document was
// only stored and retention stayed rejected with InvalidRequest.
func TestSDKPutObjectLockConfigurationExistingBucket(t *testing.T) {
	client, _ := newLockSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "olc")

	_, err := client.GetObjectLockConfiguration(ctx, &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("olc")})
	requireAPIError(t, err, 404, "ObjectLockConfigurationNotFoundError")

	requireAPIError(t, putLockConfig(client, "olc", nil), 409, "InvalidBucketState")

	enableVersioning(t, client, "olc")
	putBody(t, client, &awss3.PutObjectInput{Bucket: aws.String("olc"), Key: aws.String("old")})

	rule := &types.ObjectLockRule{DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionModeGovernance, Days: aws.Int32(1)}}
	if err := putLockConfig(client, "olc", rule); err != nil {
		t.Fatalf("PutObjectLockConfiguration: %v", err)
	}

	got, err := client.GetObjectLockConfiguration(ctx, &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("olc")})
	if err != nil {
		t.Fatalf("GetObjectLockConfiguration: %v", err)
	}

	cfg := got.ObjectLockConfiguration
	if cfg.ObjectLockEnabled != types.ObjectLockEnabledEnabled || cfg.Rule == nil ||
		cfg.Rule.DefaultRetention.Mode != types.ObjectLockRetentionModeGovernance || aws.ToInt32(cfg.Rule.DefaultRetention.Days) != 1 {
		t.Fatalf("GetObjectLockConfiguration = %+v", cfg)
	}

	// Retention on an object written before the lock was enabled now works.
	if _, err := client.PutObjectRetention(ctx, &awss3.PutObjectRetentionInput{
		Bucket: aws.String("olc"), Key: aws.String("old"),
		Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeGovernance, RetainUntilDate: aws.Time(lockEpoch().Add(time.Hour))},
	}); err != nil {
		t.Fatalf("PutObjectRetention after enabling lock: %v", err)
	}

	// A new object gets the default retention.
	putBody(t, client, &awss3.PutObjectInput{Bucket: aws.String("olc"), Key: aws.String("new")})

	ret, err := client.GetObjectRetention(ctx, &awss3.GetObjectRetentionInput{Bucket: aws.String("olc"), Key: aws.String("new")})
	if err != nil {
		t.Fatalf("GetObjectRetention: %v", err)
	}

	if ret.Retention.Mode != types.ObjectLockRetentionModeGovernance || !aws.ToTime(ret.Retention.RetainUntilDate).Equal(lockEpoch().AddDate(0, 0, 1)) {
		t.Fatalf("default retention = %+v", ret.Retention)
	}

	// Only ObjectLockEnabled clears the default rule.
	if err := putLockConfig(client, "olc", nil); err != nil {
		t.Fatalf("PutObjectLockConfiguration without rule: %v", err)
	}

	got, err = client.GetObjectLockConfiguration(ctx, &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("olc")})
	if err != nil || got.ObjectLockConfiguration.Rule != nil {
		t.Fatalf("GetObjectLockConfiguration after clearing = %+v, %v", got, err)
	}
}

func TestSDKObjectLockConfigurationCreatedWithHeader(t *testing.T) {
	client, _ := newLockSDKClient(t)

	mustCreateObjectLockBucket(t, client, "olhdr")

	got, err := client.GetObjectLockConfiguration(context.Background(), &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("olhdr")})
	if err != nil {
		t.Fatalf("GetObjectLockConfiguration on a lock bucket: %v", err)
	}

	if got.ObjectLockConfiguration.ObjectLockEnabled != types.ObjectLockEnabledEnabled {
		t.Fatalf("ObjectLockEnabled = %q, want Enabled", got.ObjectLockConfiguration.ObjectLockEnabled)
	}
}

func TestSDKObjectLockConfigurationMalformed(t *testing.T) {
	client, _ := newLockSDKClient(t)

	mustCreateObjectLockBucket(t, client, "olbad")

	for name, rule := range map[string]*types.ObjectLockRule{
		"no period":      {DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionModeGovernance}},
		"days and years": {DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionModeGovernance, Days: aws.Int32(1), Years: aws.Int32(1)}},
		"no mode":        {DefaultRetention: &types.DefaultRetention{Days: aws.Int32(1)}},
		"empty rule":     {},
	} {
		err := putLockConfig(client, "olbad", rule)
		if err == nil {
			t.Fatalf("%s: PutObjectLockConfiguration accepted", name)
		}

		requireAPIError(t, err, 400, "MalformedXML")
	}

	_, err := client.PutObjectLockConfiguration(context.Background(), &awss3.PutObjectLockConfigurationInput{
		Bucket: aws.String("olbad"), ObjectLockConfiguration: &types.ObjectLockConfiguration{},
	})
	requireAPIError(t, err, 400, "MalformedXML")
}

// TestSDKExplicitRetentionOverridesDefault puts an object with a shorter
// GOVERNANCE retention into a bucket whose default is COMPLIANCE. Real S3
// applies the explicit setting; before the fix the default was applied first
// and the header was then refused as a shortening of COMPLIANCE.
func TestSDKExplicitRetentionOverridesDefault(t *testing.T) {
	client, _ := newLockSDKClient(t)
	ctx := context.Background()

	mustCreateObjectLockBucket(t, client, "olx")

	rule := &types.ObjectLockRule{DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionModeCompliance, Days: aws.Int32(30)}}
	if err := putLockConfig(client, "olx", rule); err != nil {
		t.Fatalf("PutObjectLockConfiguration: %v", err)
	}

	until := lockEpoch().Add(time.Hour)
	out := putBody(t, client, &awss3.PutObjectInput{
		Bucket: aws.String("olx"), Key: aws.String("k"),
		ObjectLockMode: types.ObjectLockModeGovernance, ObjectLockRetainUntilDate: aws.Time(until),
	})

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("olx"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	if head.ObjectLockMode != types.ObjectLockModeGovernance || !aws.ToTime(head.ObjectLockRetainUntilDate).Equal(until) {
		t.Fatalf("lock = %s until %v, want GOVERNANCE until %v", head.ObjectLockMode, aws.ToTime(head.ObjectLockRetainUntilDate), until)
	}

	// GOVERNANCE blocks a plain delete of the version and yields to bypass.
	_, err = client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("olx"), Key: aws.String("k"), VersionId: out.VersionId})
	assertAccessDenied(t, err)

	if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("olx"), Key: aws.String("k"), VersionId: out.VersionId, BypassGovernanceRetention: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteObject with governance bypass: %v", err)
	}

	// A default COMPLIANCE version cannot be removed, even with bypass.
	def := putBody(t, client, &awss3.PutObjectInput{Bucket: aws.String("olx"), Key: aws.String("d")})

	_, err = client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String("olx"), Key: aws.String("d"), VersionId: def.VersionId, BypassGovernanceRetention: aws.Bool(true),
	})
	assertAccessDenied(t, err)

	// A mode without a retain-until date is rejected.
	_, err = client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("olx"), Key: aws.String("half"), Body: bytes.NewReader([]byte("x")),
		ObjectLockMode: types.ObjectLockModeGovernance,
	})
	requireAPIError(t, err, 400, "InvalidArgument")
}

// TestSDKCopyObjectInPlaceStorageClass changes an object's storage class with
// a copy onto itself, which S3 allows with the default COPY directive.
func TestSDKCopyObjectInPlaceStorageClass(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "inplace")

	if _, err := client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String("inplace"), Key: aws.String("k"), Body: bytes.NewReader([]byte("x")),
		Metadata: map[string]string{"keep": "me"},
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	if _, err := client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("inplace"), Key: aws.String("k"), CopySource: aws.String("inplace/k"),
		StorageClass: types.StorageClassStandardIa,
	}); err != nil {
		t.Fatalf("in-place CopyObject with a storage class: %v", err)
	}

	head, err := client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("inplace"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	if head.StorageClass != types.StorageClassStandardIa || head.Metadata["keep"] != "me" {
		t.Fatalf("after in-place copy: class %q metadata %v", head.StorageClass, head.Metadata)
	}

	// With nothing changing, the copy is still illegal.
	_, err = client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("inplace"), Key: aws.String("k"), CopySource: aws.String("inplace/k"),
	})
	requireAPIError(t, err, 400, "InvalidRequest")
}
