// restore_sdk_test.go: real aws-sdk-go-v2 tests for RestoreObject and the
// archive storage classes: GetObject/CopyObject on an unrestored GLACIER object
// (403 InvalidObjectState), the restore lifecycle through the x-amz-restore
// header, 202 vs 200, and the ListObjectsV2 RestoreStatus.
package s3_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newSettlingSDKClient is newSDKClient on a fake clock with async settling, so
// a restore is observable while it runs.
func newSettlingSDKClient(t *testing.T) (*awss3.Client, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 10, 15, 10, 30, 0, 0, time.UTC))
	cloud := cloudemu.NewAWS(config.WithClock(fc), config.WithAsyncSettle())

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{S3: cloud.S3}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	}), fc
}

// restore calls RestoreObject and returns the HTTP status.
func restore(t *testing.T, client *awss3.Client, bucket, key string, req *types.RestoreRequest) (int, error) {
	t.Helper()

	out, err := client.RestoreObject(context.Background(), &awss3.RestoreObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), RestoreRequest: req,
	})
	if err != nil {
		return 0, err
	}

	raw, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok {
		t.Fatal("RestoreObject: no raw response")
	}

	return raw.StatusCode, nil
}

func putWithClass(t *testing.T, client *awss3.Client, bucket, key string, sc types.StorageClass) {
	t.Helper()

	if _, err := client.PutObject(context.Background(), &awss3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("archived")), StorageClass: sc,
	}); err != nil {
		t.Fatalf("PutObject %s: %v", key, err)
	}
}

func headRestore(t *testing.T, client *awss3.Client, bucket, key string) string {
	t.Helper()

	out, err := client.HeadObject(context.Background(), &awss3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}

	return aws.ToString(out.Restore)
}

func TestSDKRestoreObjectLifecycle(t *testing.T) {
	client, fc := newSettlingSDKClient(t)
	ctx := context.Background()

	mustCreateBucket(t, client, "arch")
	putWithClass(t, client, "arch", "k", types.StorageClassGlacier)

	_, err := client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("arch"), Key: aws.String("k")})
	requireAPIError(t, err, 403, "InvalidObjectState")

	_, err = client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket: aws.String("arch"), Key: aws.String("copy"), CopySource: aws.String("arch/k"),
	})
	requireAPIError(t, err, 403, "InvalidObjectState")

	if got := headRestore(t, client, "arch", "k"); got != "" {
		t.Fatalf("x-amz-restore before restore = %q, want none", got)
	}

	req := &types.RestoreRequest{Days: aws.Int32(3), GlacierJobParameters: &types.GlacierJobParameters{Tier: types.TierStandard}}

	status, err := restore(t, client, "arch", "k", req)
	if err != nil || status != 202 {
		t.Fatalf("first RestoreObject = %d, %v; want 202", status, err)
	}

	if got := headRestore(t, client, "arch", "k"); got != `ongoing-request="true"` {
		t.Fatalf("x-amz-restore while running = %q", got)
	}

	_, err = restore(t, client, "arch", "k", req)
	requireAPIError(t, err, 409, "RestoreAlreadyInProgress")

	fc.Advance(5 * time.Second)

	want := `ongoing-request="false", expiry-date="Sun, 19 Oct 2025 00:00:00 GMT"`
	if got := headRestore(t, client, "arch", "k"); got != want {
		t.Fatalf("x-amz-restore after settle = %q, want %q", got, want)
	}

	obj, err := client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("arch"), Key: aws.String("k")})
	if err != nil {
		t.Fatalf("GetObject after restore: %v", err)
	}

	body, _ := io.ReadAll(obj.Body)
	_ = obj.Body.Close()

	if string(body) != "archived" || aws.ToString(obj.Restore) != want || obj.StorageClass != types.StorageClassGlacier {
		t.Fatalf("GetObject = %q restore %q class %q", body, aws.ToString(obj.Restore), obj.StorageClass)
	}

	status, err = restore(t, client, "arch", "k", &types.RestoreRequest{Days: aws.Int32(10)})
	if err != nil || status != 200 {
		t.Fatalf("RestoreObject on a restored copy = %d, %v; want 200", status, err)
	}

	if got := headRestore(t, client, "arch", "k"); !strings.Contains(got, `expiry-date="Sun, 26 Oct 2025 00:00:00 GMT"`) {
		t.Fatalf("x-amz-restore after extend = %q", got)
	}

	list, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String("arch"), OptionalObjectAttributes: []types.OptionalObjectAttributes{types.OptionalObjectAttributesRestoreStatus},
	})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}

	if len(list.Contents) != 1 || list.Contents[0].RestoreStatus == nil ||
		aws.ToBool(list.Contents[0].RestoreStatus.IsRestoreInProgress) || list.Contents[0].RestoreStatus.RestoreExpiryDate == nil {
		t.Fatalf("ListObjectsV2 RestoreStatus = %+v", list.Contents)
	}

	plain, err := client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("arch")})
	if err != nil {
		t.Fatalf("ListObjectsV2 plain: %v", err)
	}

	if plain.Contents[0].RestoreStatus != nil {
		t.Fatal("RestoreStatus listed without being requested")
	}
}

func TestSDKRestoreObjectErrors(t *testing.T) {
	client, _ := newSettlingSDKClient(t)

	mustCreateBucket(t, client, "arch2")
	putWithClass(t, client, "arch2", "std", "")
	putWithClass(t, client, "arch2", "it", types.StorageClassIntelligentTiering)
	putWithClass(t, client, "arch2", "deep", types.StorageClassDeepArchive)

	_, err := restore(t, client, "arch2", "std", &types.RestoreRequest{Days: aws.Int32(1)})
	requireAPIError(t, err, 403, "InvalidObjectState")

	_, err = restore(t, client, "arch2", "it", &types.RestoreRequest{})
	requireAPIError(t, err, 403, "ObjectAlreadyInActiveTierError")

	_, err = restore(t, client, "arch2", "it", &types.RestoreRequest{Days: aws.Int32(1)})
	requireAPIError(t, err, 400, "InvalidArgument")

	_, err = restore(t, client, "arch2", "deep", &types.RestoreRequest{
		Days: aws.Int32(1), GlacierJobParameters: &types.GlacierJobParameters{Tier: types.TierExpedited},
	})
	requireAPIError(t, err, 400, "InvalidArgument")

	_, err = restore(t, client, "arch2", "deep", &types.RestoreRequest{})
	requireAPIError(t, err, 400, "MalformedXML")

	_, err = restore(t, client, "arch2", "missing", &types.RestoreRequest{Days: aws.Int32(1)})
	requireAPIError(t, err, 404, "NoSuchKey")
}
