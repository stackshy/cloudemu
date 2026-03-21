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
