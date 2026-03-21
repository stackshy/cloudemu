package secrets

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/secretsmanager"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stackshy/cloudemu/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSecrets(opts ...Option) *Secrets {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewSecrets(secretsmanager.New(o), opts...)
}

func createViaPortable(t *testing.T, s *Secrets, name, value string) *driver.SecretInfo {
	t.Helper()

	cfg := driver.SecretConfig{Name: name, Description: "test"}

	info, err := s.CreateSecret(context.Background(), cfg, []byte(value))
	require.NoError(t, err)

	return info
}

func TestBasicCreateAndGet(t *testing.T) {
	s := newTestSecrets()

	info := createViaPortable(t, s, "basic-secret", "hello")

	assert.NotEmpty(t, info.ID)
	assert.Equal(t, "basic-secret", info.Name)

	got, err := s.GetSecret(context.Background(), "basic-secret")
	require.NoError(t, err)
	assert.Equal(t, info.Name, got.Name)
}

func TestBasicDeleteSecret(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "del-secret", "val")

	err := s.DeleteSecret(context.Background(), "del-secret")
	require.NoError(t, err)

	_, err = s.GetSecret(context.Background(), "del-secret")
	require.Error(t, err)
}

func TestBasicListSecrets(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "ls1", "v1")
	createViaPortable(t, s, "ls2", "v2")

	list, err := s.ListSecrets(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, len(list))
}

func TestBasicPutAndGetValue(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "pv-secret", "initial")

	ver, err := s.PutSecretValue(context.Background(), "pv-secret", []byte("updated"))
	require.NoError(t, err)
	assert.Equal(t, true, ver.Current)

	got, err := s.GetSecretValue(context.Background(), "pv-secret", "")
	require.NoError(t, err)
	assert.Equal(t, "updated", string(got.Value))
}

func TestBasicListVersions(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "vlv-secret", "v1")

	_, err := s.PutSecretValue(context.Background(), "vlv-secret", []byte("v2"))
	require.NoError(t, err)

	versions, err := s.ListSecretVersions(context.Background(), "vlv-secret")
	require.NoError(t, err)
	assert.Equal(t, 2, len(versions))
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	s := newTestSecrets(WithRecorder(rec))

	createViaPortable(t, s, "rec-secret", "val")

	_, err := s.GetSecret(context.Background(), "rec-secret")
	require.NoError(t, err)

	calls := rec.Calls()
	require.GreaterOrEqual(t, len(calls), 2, "expected at least 2 recorded calls")

	assert.Equal(t, "secrets", calls[0].Service)
	assert.Equal(t, "CreateSecret", calls[0].Operation)
	assert.Equal(t, "secrets", calls[1].Service)
	assert.Equal(t, "GetSecret", calls[1].Operation)
}

func TestWithRecorderRecordsAllOps(t *testing.T) {
	rec := recorder.New()
	s := newTestSecrets(WithRecorder(rec))
	ctx := context.Background()

	createViaPortable(t, s, "all-ops", "v1")

	_, err := s.GetSecret(ctx, "all-ops")
	require.NoError(t, err)

	_, err = s.ListSecrets(ctx)
	require.NoError(t, err)

	_, err = s.PutSecretValue(ctx, "all-ops", []byte("v2"))
	require.NoError(t, err)

	_, err = s.GetSecretValue(ctx, "all-ops", "")
	require.NoError(t, err)

	_, err = s.ListSecretVersions(ctx, "all-ops")
	require.NoError(t, err)

	err = s.DeleteSecret(ctx, "all-ops")
	require.NoError(t, err)

	assert.Equal(t, 7, rec.CallCount())
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	createViaPortable(t, s, "met-secret", "val")

	_, err := s.GetSecret(context.Background(), "met-secret")
	require.NoError(t, err)

	all := mc.All()
	require.NotEmpty(t, all, "expected metrics to be collected")

	q := metrics.NewQuery(mc)
	callCount := q.ByName("calls_total").Count()

	assert.GreaterOrEqual(t, callCount, 2, "expected at least 2 calls_total metrics")
}

func TestWithMetricsRecordsErrors(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	_, err := s.GetSecret(context.Background(), "nonexistent")
	require.Error(t, err)

	q := metrics.NewQuery(mc)
	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithMetricsHistogram(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	createViaPortable(t, s, "hist-secret", "val")

	q := metrics.NewQuery(mc)
	histCount := q.ByName("call_duration").Count()

	assert.GreaterOrEqual(t, histCount, 1, "expected at least 1 histogram metric")
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestSecrets(WithErrorInjection(inj))

	injErr := fmt.Errorf("injected failure")
	inj.Set("secrets", "CreateSecret", injErr, inject.Always{})

	cfg := driver.SecretConfig{Name: "inj-secret"}
	_, err := s.CreateSecret(context.Background(), cfg, []byte("val"))

	require.Error(t, err)
}

func TestWithErrorInjectionSelectiveOps(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestSecrets(WithErrorInjection(inj))

	createViaPortable(t, s, "sel-secret", "val")

	injErr := fmt.Errorf("get fails")
	inj.Set("secrets", "GetSecret", injErr, inject.Always{})

	_, err := s.GetSecret(context.Background(), "sel-secret")
	require.Error(t, err)

	list, err := s.ListSecrets(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, len(list))
}

func TestWithErrorInjectionCountdown(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestSecrets(WithErrorInjection(inj))

	injErr := fmt.Errorf("countdown error")
	inj.Set("secrets", "ListSecrets", injErr, inject.NewCountdown(1))

	_, err := s.ListSecrets(context.Background())
	require.Error(t, err)

	_, err = s.ListSecrets(context.Background())
	require.NoError(t, err)
}

func TestWithRecorderAndMetricsCombined(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	s := newTestSecrets(WithRecorder(rec), WithMetrics(mc))

	createViaPortable(t, s, "combo-secret", "val")

	assert.Equal(t, 1, rec.CallCount())

	assert.NotEmpty(t, mc.All(), "expected metrics to be collected alongside recorder")
}

func TestWithErrorInjectionAndRecorder(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	s := newTestSecrets(WithRecorder(rec), WithErrorInjection(inj))

	injErr := fmt.Errorf("injected")
	inj.Set("secrets", "CreateSecret", injErr, inject.Always{})

	cfg := driver.SecretConfig{Name: "fail"}
	_, err := s.CreateSecret(context.Background(), cfg, []byte("val"))

	require.Error(t, err)
	assert.Equal(t, 1, rec.CallCount())

	last := rec.LastCall()
	require.NotNil(t, last, "expected last call to be recorded")

	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestGetSecretValueThroughPortable(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "gsv-secret", "initial")

	v2, err := s.PutSecretValue(context.Background(), "gsv-secret", []byte("v2"))
	require.NoError(t, err)

	got, err := s.GetSecretValue(context.Background(), "gsv-secret", v2.VersionID)
	require.NoError(t, err)

	assert.Equal(t, v2.VersionID, got.VersionID)
	assert.Equal(t, "v2", string(got.Value))
}

func TestDeleteSecretNotFoundThroughPortable(t *testing.T) {
	s := newTestSecrets()

	err := s.DeleteSecret(context.Background(), "no-such")

	require.Error(t, err)
}

func TestPutSecretValueNotFoundThroughPortable(t *testing.T) {
	s := newTestSecrets()

	_, err := s.PutSecretValue(context.Background(), "nope", []byte("val"))

	require.Error(t, err)
}

func TestListSecretVersionsNotFoundThroughPortable(t *testing.T) {
	s := newTestSecrets()

	_, err := s.ListSecretVersions(context.Background(), "nope")

	require.Error(t, err)
}

func TestWithMetricsLabels(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	createViaPortable(t, s, "label-secret", "val")

	q := metrics.NewQuery(mc)

	ct := q.ByName("calls_total").
		ByLabel("service", "secrets").
		ByLabel("operation", "CreateSecret").
		Count()

	assert.Equal(t, 1, ct)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	lim := ratelimit.New(1, 1, fc)
	s := NewSecrets(secretsmanager.New(o), WithRateLimiter(lim))

	createViaPortable(t, s, "rl-secret", "val")

	_, err := s.GetSecret(context.Background(), "rl-secret")
	require.Error(t, err)
}

func TestWithLatency(t *testing.T) {
	s := newTestSecrets(WithLatency(time.Millisecond))

	start := time.Now()

	createViaPortable(t, s, "lat-secret", "val")

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, time.Millisecond, "expected latency to be applied")
}

func TestGetSecretValueErrorPath(t *testing.T) {
	s := newTestSecrets()

	_, err := s.GetSecretValue(context.Background(), "missing", "")

	require.Error(t, err)
}

func TestWithRateLimiterAndRecorder(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	lim := ratelimit.New(1, 1, fc)
	rec := recorder.New()
	s := NewSecrets(secretsmanager.New(o), WithRateLimiter(lim), WithRecorder(rec))

	createViaPortable(t, s, "rlr-secret", "val")

	_, err := s.ListSecrets(context.Background())
	require.Error(t, err)

	assert.Equal(t, 2, rec.CallCount())
}
