package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/s3"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/storage/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDriver() (driver.Bucket, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return s3.New(opts), fc
}

func newTestBucket(opts ...Option) (*Bucket, *config.FakeClock) {
	d, fc := newTestDriver()
	return NewBucket(d, opts...), fc
}

func setupBucketWithObject(t *testing.T, b *Bucket) {
	t.Helper()

	ctx := context.Background()

	err := b.CreateBucket(ctx, "test-bucket")
	require.NoError(t, err)

	err = b.PutObject(ctx, "test-bucket", "key1", []byte("value1"), "text/plain", nil)
	require.NoError(t, err)
}

func TestNewBucket(t *testing.T) {
	b, _ := newTestBucket()

	require.NotNil(t, b)
	require.NotNil(t, b.driver)
}

func TestCreateBucketPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		err := b.CreateBucket(ctx, "my-bucket")
		require.NoError(t, err)
	})

	t.Run("duplicate error", func(t *testing.T) {
		err := b.CreateBucket(ctx, "my-bucket")
		require.Error(t, err)
	})
}

func TestDeleteBucketPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "del-bucket")
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := b.DeleteBucket(ctx, "del-bucket")
		require.NoError(t, delErr)
	})

	t.Run("not found", func(t *testing.T) {
		delErr := b.DeleteBucket(ctx, "nonexistent")
		require.Error(t, delErr)
	})
}

func TestListBucketsPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	buckets, err := b.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(buckets))

	err = b.CreateBucket(ctx, "a")
	require.NoError(t, err)

	err = b.CreateBucket(ctx, "b")
	require.NoError(t, err)

	buckets, err = b.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(buckets))
}

func TestPutGetDeleteObjectPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	setupBucketWithObject(t, b)

	t.Run("get existing object", func(t *testing.T) {
		obj, err := b.GetObject(ctx, "test-bucket", "key1")
		require.NoError(t, err)
		assert.Equal(t, "key1", obj.Info.Key)
		assert.Equal(t, string([]byte("value1")), string(obj.Data))
	})

	t.Run("delete object", func(t *testing.T) {
		err := b.DeleteObject(ctx, "test-bucket", "key1")
		require.NoError(t, err)

		_, getErr := b.GetObject(ctx, "test-bucket", "key1")
		require.Error(t, getErr)
	})
}

func TestHeadObjectPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	setupBucketWithObject(t, b)

	info, err := b.HeadObject(ctx, "test-bucket", "key1")
	require.NoError(t, err)
	assert.Equal(t, "key1", info.Key)
}

func TestListObjectsPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	setupBucketWithObject(t, b)

	result, err := b.ListObjects(ctx, "test-bucket", driver.ListOptions{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Objects), 1)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	b, _ := newTestBucket(WithRecorder(rec))
	ctx := context.Background()

	err := b.CreateBucket(ctx, "rec-bucket")
	require.NoError(t, err)

	err = b.PutObject(ctx, "rec-bucket", "k", []byte("v"), "text/plain", nil)
	require.NoError(t, err)

	_, err = b.GetObject(ctx, "rec-bucket", "k")
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 3)

	createCalls := rec.CallCountFor("storage", "CreateBucket")
	assert.Equal(t, 1, createCalls)

	putCalls := rec.CallCountFor("storage", "PutObject")
	assert.Equal(t, 1, putCalls)

	getCalls := rec.CallCountFor("storage", "GetObject")
	assert.Equal(t, 1, getCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	b, _ := newTestBucket(WithRecorder(rec))
	ctx := context.Background()

	_, _ = b.GetObject(ctx, "nonexistent", "k")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	b, _ := newTestBucket(WithMetrics(mc))
	ctx := context.Background()

	err := b.CreateBucket(ctx, "met-bucket")
	require.NoError(t, err)

	err = b.PutObject(ctx, "met-bucket", "k", []byte("v"), "text/plain", nil)
	require.NoError(t, err)

	_, err = b.GetObject(ctx, "met-bucket", "k")
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 3)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 3)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	b, _ := newTestBucket(WithMetrics(mc))
	ctx := context.Background()

	_, _ = b.GetObject(ctx, "nonexistent", "k")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	b, _ := newTestBucket(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("storage", "CreateBucket", injectedErr, inject.Always{})

	err := b.CreateBucket(ctx, "fail-bucket")
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	b, _ := newTestBucket(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("storage", "PutObject", injectedErr, inject.Always{})

	err := b.CreateBucket(ctx, "inj-bucket")
	require.NoError(t, err)

	err = b.PutObject(ctx, "inj-bucket", "k", []byte("v"), "text/plain", nil)
	require.Error(t, err)

	putCalls := rec.CallsFor("storage", "PutObject")
	assert.Equal(t, 1, len(putCalls))
	assert.NotNil(t, putCalls[0].Error, "expected recorded PutObject call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	b, _ := newTestBucket(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("storage", "CreateBucket", injectedErr, inject.Always{})

	err := b.CreateBucket(ctx, "test")
	require.Error(t, err)

	inj.Remove("storage", "CreateBucket")

	err = b.CreateBucket(ctx, "test")
	require.NoError(t, err)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	d := s3.New(opts)
	limiter := ratelimit.New(1, 1, fc)
	b := NewBucket(d, WithRateLimiter(limiter))
	ctx := context.Background()

	err := b.CreateBucket(ctx, "rl-bucket")
	require.NoError(t, err)

	err = b.DeleteBucket(ctx, "rl-bucket")
	require.Error(t, err, "expected rate limit error on second call without time advance")
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	b, _ := newTestBucket(WithLatency(latency))
	ctx := context.Background()

	err := b.CreateBucket(ctx, "lat-bucket")
	require.NoError(t, err)

	buckets, err := b.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(buckets))
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	b, _ := newTestBucket(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	err := b.CreateBucket(ctx, "all-opts")
	require.NoError(t, err)

	err = b.PutObject(ctx, "all-opts", "k", []byte("v"), "text/plain", nil)
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableGetObjectError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	_, err := b.GetObject(ctx, "no-bucket", "k")
	require.Error(t, err)
}

func TestPortableDeleteObjectError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.DeleteObject(ctx, "no-bucket", "k")
	require.Error(t, err)
}

func TestPortableHeadObjectError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	_, err := b.HeadObject(ctx, "no-bucket", "k")
	require.Error(t, err)
}

func TestBucketPolicyPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "pol-bkt")
	require.NoError(t, err)

	policy := driver.BucketPolicy{
		Version: "2012-10-17",
		Statements: []driver.PolicyStatement{
			{Effect: "Allow", Principal: "*", Actions: []string{"s3:GetObject"}, Resources: []string{"arn:aws:s3:::pol-bkt/*"}},
		},
	}

	err = b.PutBucketPolicy(ctx, "pol-bkt", policy)
	require.NoError(t, err)

	got, err := b.GetBucketPolicy(ctx, "pol-bkt")
	require.NoError(t, err)
	assert.Equal(t, "2012-10-17", got.Version)
	assert.Equal(t, 1, len(got.Statements))

	err = b.DeleteBucketPolicy(ctx, "pol-bkt")
	require.NoError(t, err)

	_, err = b.GetBucketPolicy(ctx, "pol-bkt")
	require.Error(t, err)
}

func TestBucketPolicyPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.PutBucketPolicy(ctx, "no-bkt", driver.BucketPolicy{})
	require.Error(t, err)

	_, err = b.GetBucketPolicy(ctx, "no-bkt")
	require.Error(t, err)

	err = b.DeleteBucketPolicy(ctx, "no-bkt")
	require.Error(t, err)
}

func TestObjectTaggingPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()
	setupBucketWithObject(t, b)

	tags, err := b.GetObjectTagging(ctx, "test-bucket", "key1")
	require.NoError(t, err)
	assert.Equal(t, 0, len(tags))

	err = b.PutObjectTagging(ctx, "test-bucket", "key1", map[string]string{"env": "prod", "team": "eng"})
	require.NoError(t, err)

	tags, err = b.GetObjectTagging(ctx, "test-bucket", "key1")
	require.NoError(t, err)
	assert.Equal(t, 2, len(tags))
	assert.Equal(t, "prod", tags["env"])

	err = b.DeleteObjectTagging(ctx, "test-bucket", "key1")
	require.NoError(t, err)

	tags, err = b.GetObjectTagging(ctx, "test-bucket", "key1")
	require.NoError(t, err)
	assert.Equal(t, 0, len(tags))
}

func TestObjectTaggingPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.PutObjectTagging(ctx, "no-bkt", "k", map[string]string{"a": "b"})
	require.Error(t, err)

	_, err = b.GetObjectTagging(ctx, "no-bkt", "k")
	require.Error(t, err)

	err = b.DeleteObjectTagging(ctx, "no-bkt", "k")
	require.Error(t, err)
}

func TestBucketTaggingPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "tag-bkt")
	require.NoError(t, err)

	tags, err := b.GetBucketTagging(ctx, "tag-bkt")
	require.NoError(t, err)
	assert.Equal(t, 0, len(tags))

	err = b.PutBucketTagging(ctx, "tag-bkt", map[string]string{"project": "cloudemu"})
	require.NoError(t, err)

	tags, err = b.GetBucketTagging(ctx, "tag-bkt")
	require.NoError(t, err)
	assert.Equal(t, 1, len(tags))
	assert.Equal(t, "cloudemu", tags["project"])

	err = b.DeleteBucketTagging(ctx, "tag-bkt")
	require.NoError(t, err)

	tags, err = b.GetBucketTagging(ctx, "tag-bkt")
	require.NoError(t, err)
	assert.Equal(t, 0, len(tags))
}

func TestBucketTaggingPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.PutBucketTagging(ctx, "no-bkt", map[string]string{"a": "b"})
	require.Error(t, err)

	_, err = b.GetBucketTagging(ctx, "no-bkt")
	require.Error(t, err)

	err = b.DeleteBucketTagging(ctx, "no-bkt")
	require.Error(t, err)
}

func TestCORSConfigPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "cors-bkt")
	require.NoError(t, err)

	cfg := driver.CORSConfig{
		Rules: []driver.CORSRule{
			{AllowedOrigins: []string{"https://example.com"}, AllowedMethods: []string{"GET", "PUT"}, MaxAgeSeconds: 300},
		},
	}

	err = b.PutCORSConfig(ctx, "cors-bkt", cfg)
	require.NoError(t, err)

	got, err := b.GetCORSConfig(ctx, "cors-bkt")
	require.NoError(t, err)
	assert.Equal(t, 1, len(got.Rules))
	assert.Equal(t, 300, got.Rules[0].MaxAgeSeconds)

	err = b.DeleteCORSConfig(ctx, "cors-bkt")
	require.NoError(t, err)

	_, err = b.GetCORSConfig(ctx, "cors-bkt")
	require.Error(t, err)
}

func TestCORSConfigPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.PutCORSConfig(ctx, "no-bkt", driver.CORSConfig{})
	require.Error(t, err)

	_, err = b.GetCORSConfig(ctx, "no-bkt")
	require.Error(t, err)

	err = b.DeleteCORSConfig(ctx, "no-bkt")
	require.Error(t, err)
}

func TestEncryptionConfigPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "enc-bkt")
	require.NoError(t, err)

	cfg := driver.EncryptionConfig{Enabled: true, Algorithm: "AES256"}

	err = b.PutEncryptionConfig(ctx, "enc-bkt", cfg)
	require.NoError(t, err)

	got, err := b.GetEncryptionConfig(ctx, "enc-bkt")
	require.NoError(t, err)
	assert.Equal(t, true, got.Enabled)
	assert.Equal(t, "AES256", got.Algorithm)

	_, err = b.GetEncryptionConfig(ctx, "no-bkt")
	require.Error(t, err)
}

func TestEncryptionConfigPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.PutEncryptionConfig(ctx, "no-bkt", driver.EncryptionConfig{})
	require.Error(t, err)

	_, err = b.GetEncryptionConfig(ctx, "no-bkt")
	require.Error(t, err)
}

func TestCopyObjectPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()
	setupBucketWithObject(t, b)

	err := b.CreateBucket(ctx, "dst-bucket")
	require.NoError(t, err)

	err = b.CopyObject(ctx, "dst-bucket", "copied-key", driver.CopySource{Bucket: "test-bucket", Key: "key1"})
	require.NoError(t, err)

	obj, err := b.GetObject(ctx, "dst-bucket", "copied-key")
	require.NoError(t, err)
	assert.Equal(t, "value1", string(obj.Data))
}

func TestCopyObjectPortableError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CopyObject(ctx, "no-bkt", "k", driver.CopySource{Bucket: "no-src", Key: "k"})
	require.Error(t, err)
}

func TestPresignedURLPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()
	setupBucketWithObject(t, b)

	url, err := b.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: "test-bucket", Key: "key1", Method: "GET", ExpiresIn: time.Minute,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, url.URL)
	assert.Equal(t, "GET", url.Method)
}

func TestPresignedURLPortableError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	_, err := b.GeneratePresignedURL(ctx, driver.PresignedURLRequest{
		Bucket: "no-bkt", Key: "k", Method: "GET", ExpiresIn: time.Minute,
	})
	require.Error(t, err)
}

func TestLifecycleConfigPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "lc-bkt")
	require.NoError(t, err)

	cfg := driver.LifecycleConfig{
		Rules: []driver.LifecycleRule{{ID: "expire-30", Enabled: true, ExpirationDays: 30}},
	}

	err = b.PutLifecycleConfig(ctx, "lc-bkt", cfg)
	require.NoError(t, err)

	got, err := b.GetLifecycleConfig(ctx, "lc-bkt")
	require.NoError(t, err)
	assert.Equal(t, 1, len(got.Rules))
	assert.Equal(t, "expire-30", got.Rules[0].ID)
}

func TestLifecycleConfigPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.PutLifecycleConfig(ctx, "no-bkt", driver.LifecycleConfig{})
	require.Error(t, err)

	_, err = b.GetLifecycleConfig(ctx, "no-bkt")
	require.Error(t, err)
}

func TestEvaluateLifecyclePortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "eval-bkt")
	require.NoError(t, err)

	keys, err := b.EvaluateLifecycle(ctx, "eval-bkt")
	require.NoError(t, err)
	assert.Equal(t, 0, len(keys))
}

func TestEvaluateLifecyclePortableError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	_, err := b.EvaluateLifecycle(ctx, "no-bkt")
	require.Error(t, err)
}

func TestMultipartUploadPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "mp-bkt")
	require.NoError(t, err)

	mp, err := b.CreateMultipartUpload(ctx, "mp-bkt", "bigfile.bin", "application/octet-stream")
	require.NoError(t, err)
	assert.NotEmpty(t, mp.UploadID)

	part1, err := b.UploadPart(ctx, "mp-bkt", "bigfile.bin", mp.UploadID, 1, []byte("AAAA"))
	require.NoError(t, err)
	assert.Equal(t, 1, part1.PartNumber)

	part2, err := b.UploadPart(ctx, "mp-bkt", "bigfile.bin", mp.UploadID, 2, []byte("BBBB"))
	require.NoError(t, err)

	err = b.CompleteMultipartUpload(ctx, "mp-bkt", "bigfile.bin", mp.UploadID, []driver.UploadPart{*part1, *part2})
	require.NoError(t, err)

	obj, err := b.GetObject(ctx, "mp-bkt", "bigfile.bin")
	require.NoError(t, err)
	assert.Equal(t, "AAAABBBB", string(obj.Data))
}

func TestAbortMultipartUploadPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "abort-bkt")
	require.NoError(t, err)

	mp, err := b.CreateMultipartUpload(ctx, "abort-bkt", "file.bin", "application/octet-stream")
	require.NoError(t, err)

	err = b.AbortMultipartUpload(ctx, "abort-bkt", "file.bin", mp.UploadID)
	require.NoError(t, err)
}

func TestListMultipartUploadsPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "list-mp-bkt")
	require.NoError(t, err)

	uploads, err := b.ListMultipartUploads(ctx, "list-mp-bkt")
	require.NoError(t, err)
	assert.Equal(t, 0, len(uploads))
}

func TestListMultipartUploadsPortableError(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	_, err := b.ListMultipartUploads(ctx, "no-bkt")
	require.Error(t, err)
}

func TestVersioningPortable(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.CreateBucket(ctx, "ver-bkt")
	require.NoError(t, err)

	enabled, err := b.GetBucketVersioning(ctx, "ver-bkt")
	require.NoError(t, err)
	assert.Equal(t, false, enabled)

	err = b.SetBucketVersioning(ctx, "ver-bkt", true)
	require.NoError(t, err)

	enabled, err = b.GetBucketVersioning(ctx, "ver-bkt")
	require.NoError(t, err)
	assert.Equal(t, true, enabled)
}

func TestVersioningPortableErrors(t *testing.T) {
	b, _ := newTestBucket()
	ctx := context.Background()

	err := b.SetBucketVersioning(ctx, "no-bkt", true)
	require.Error(t, err)

	_, err = b.GetBucketVersioning(ctx, "no-bkt")
	require.Error(t, err)
}
