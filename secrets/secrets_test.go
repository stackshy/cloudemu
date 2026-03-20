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
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func newTestSecrets(opts ...Option) *Secrets {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return NewSecrets(secretsmanager.New(o), opts...)
}

func createViaPortable(t *testing.T, s *Secrets, name, value string) *driver.SecretInfo {
	t.Helper()

	cfg := driver.SecretConfig{Name: name, Description: "test"}

	info, err := s.CreateSecret(context.Background(), cfg, []byte(value))
	requireNoError(t, err)

	return info
}

func TestBasicCreateAndGet(t *testing.T) {
	s := newTestSecrets()

	info := createViaPortable(t, s, "basic-secret", "hello")

	assertNotEmpty(t, info.ID)
	assertEqual(t, "basic-secret", info.Name)

	got, err := s.GetSecret(context.Background(), "basic-secret")
	requireNoError(t, err)
	assertEqual(t, info.Name, got.Name)
}

func TestBasicDeleteSecret(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "del-secret", "val")

	err := s.DeleteSecret(context.Background(), "del-secret")
	requireNoError(t, err)

	_, err = s.GetSecret(context.Background(), "del-secret")
	assertError(t, err, true)
}

func TestBasicListSecrets(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "ls1", "v1")
	createViaPortable(t, s, "ls2", "v2")

	list, err := s.ListSecrets(context.Background())
	requireNoError(t, err)
	assertEqual(t, 2, len(list))
}

func TestBasicPutAndGetValue(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "pv-secret", "initial")

	ver, err := s.PutSecretValue(context.Background(), "pv-secret", []byte("updated"))
	requireNoError(t, err)
	assertEqual(t, true, ver.Current)

	got, err := s.GetSecretValue(context.Background(), "pv-secret", "")
	requireNoError(t, err)
	assertEqual(t, "updated", string(got.Value))
}

func TestBasicListVersions(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "vlv-secret", "v1")

	_, err := s.PutSecretValue(context.Background(), "vlv-secret", []byte("v2"))
	requireNoError(t, err)

	versions, err := s.ListSecretVersions(context.Background(), "vlv-secret")
	requireNoError(t, err)
	assertEqual(t, 2, len(versions))
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	s := newTestSecrets(WithRecorder(rec))

	createViaPortable(t, s, "rec-secret", "val")

	_, err := s.GetSecret(context.Background(), "rec-secret")
	requireNoError(t, err)

	calls := rec.Calls()
	if len(calls) < 2 {
		t.Fatalf("expected at least 2 recorded calls, got %d", len(calls))
	}

	assertEqual(t, "secrets", calls[0].Service)
	assertEqual(t, "CreateSecret", calls[0].Operation)
	assertEqual(t, "secrets", calls[1].Service)
	assertEqual(t, "GetSecret", calls[1].Operation)
}

func TestWithRecorderRecordsAllOps(t *testing.T) {
	rec := recorder.New()
	s := newTestSecrets(WithRecorder(rec))
	ctx := context.Background()

	createViaPortable(t, s, "all-ops", "v1")

	_, err := s.GetSecret(ctx, "all-ops")
	requireNoError(t, err)

	_, err = s.ListSecrets(ctx)
	requireNoError(t, err)

	_, err = s.PutSecretValue(ctx, "all-ops", []byte("v2"))
	requireNoError(t, err)

	_, err = s.GetSecretValue(ctx, "all-ops", "")
	requireNoError(t, err)

	_, err = s.ListSecretVersions(ctx, "all-ops")
	requireNoError(t, err)

	err = s.DeleteSecret(ctx, "all-ops")
	requireNoError(t, err)

	assertEqual(t, 7, rec.CallCount())
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	createViaPortable(t, s, "met-secret", "val")

	_, err := s.GetSecret(context.Background(), "met-secret")
	requireNoError(t, err)

	all := mc.All()
	if len(all) == 0 {
		t.Fatal("expected metrics to be collected")
	}

	q := metrics.NewQuery(mc)
	callCount := q.ByName("calls_total").Count()

	if callCount < 2 {
		t.Errorf("expected at least 2 calls_total metrics, got %d", callCount)
	}
}

func TestWithMetricsRecordsErrors(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	_, err := s.GetSecret(context.Background(), "nonexistent")
	assertError(t, err, true)

	q := metrics.NewQuery(mc)
	errCount := q.ByName("errors_total").Count()
	assertEqual(t, 1, errCount)
}

func TestWithMetricsHistogram(t *testing.T) {
	mc := metrics.NewCollector()
	s := newTestSecrets(WithMetrics(mc))

	createViaPortable(t, s, "hist-secret", "val")

	q := metrics.NewQuery(mc)
	histCount := q.ByName("call_duration").Count()

	if histCount < 1 {
		t.Error("expected at least 1 histogram metric")
	}
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestSecrets(WithErrorInjection(inj))

	injErr := fmt.Errorf("injected failure")
	inj.Set("secrets", "CreateSecret", injErr, inject.Always{})

	cfg := driver.SecretConfig{Name: "inj-secret"}
	_, err := s.CreateSecret(context.Background(), cfg, []byte("val"))

	assertError(t, err, true)
}

func TestWithErrorInjectionSelectiveOps(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestSecrets(WithErrorInjection(inj))

	createViaPortable(t, s, "sel-secret", "val")

	injErr := fmt.Errorf("get fails")
	inj.Set("secrets", "GetSecret", injErr, inject.Always{})

	_, err := s.GetSecret(context.Background(), "sel-secret")
	assertError(t, err, true)

	list, err := s.ListSecrets(context.Background())
	requireNoError(t, err)
	assertEqual(t, 1, len(list))
}

func TestWithErrorInjectionCountdown(t *testing.T) {
	inj := inject.NewInjector()
	s := newTestSecrets(WithErrorInjection(inj))

	injErr := fmt.Errorf("countdown error")
	inj.Set("secrets", "ListSecrets", injErr, inject.NewCountdown(1))

	_, err := s.ListSecrets(context.Background())
	assertError(t, err, true)

	_, err = s.ListSecrets(context.Background())
	requireNoError(t, err)
}

func TestWithRecorderAndMetricsCombined(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	s := newTestSecrets(WithRecorder(rec), WithMetrics(mc))

	createViaPortable(t, s, "combo-secret", "val")

	assertEqual(t, 1, rec.CallCount())

	if len(mc.All()) == 0 {
		t.Error("expected metrics to be collected alongside recorder")
	}
}

func TestWithErrorInjectionAndRecorder(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	s := newTestSecrets(WithRecorder(rec), WithErrorInjection(inj))

	injErr := fmt.Errorf("injected")
	inj.Set("secrets", "CreateSecret", injErr, inject.Always{})

	cfg := driver.SecretConfig{Name: "fail"}
	_, err := s.CreateSecret(context.Background(), cfg, []byte("val"))

	assertError(t, err, true)
	assertEqual(t, 1, rec.CallCount())

	last := rec.LastCall()
	if last == nil {
		t.Fatal("expected last call to be recorded")
	}

	if last.Error == nil {
		t.Error("expected recorded call to have an error")
	}
}

func TestGetSecretValueThroughPortable(t *testing.T) {
	s := newTestSecrets()

	createViaPortable(t, s, "gsv-secret", "initial")

	v2, err := s.PutSecretValue(context.Background(), "gsv-secret", []byte("v2"))
	requireNoError(t, err)

	got, err := s.GetSecretValue(context.Background(), "gsv-secret", v2.VersionID)
	requireNoError(t, err)

	assertEqual(t, v2.VersionID, got.VersionID)
	assertEqual(t, "v2", string(got.Value))
}

func TestDeleteSecretNotFoundThroughPortable(t *testing.T) {
	s := newTestSecrets()

	err := s.DeleteSecret(context.Background(), "no-such")

	assertError(t, err, true)
}

func TestPutSecretValueNotFoundThroughPortable(t *testing.T) {
	s := newTestSecrets()

	_, err := s.PutSecretValue(context.Background(), "nope", []byte("val"))

	assertError(t, err, true)
}

func TestListSecretVersionsNotFoundThroughPortable(t *testing.T) {
	s := newTestSecrets()

	_, err := s.ListSecretVersions(context.Background(), "nope")

	assertError(t, err, true)
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

	assertEqual(t, 1, ct)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	lim := ratelimit.New(1, 1, fc)
	s := NewSecrets(secretsmanager.New(o), WithRateLimiter(lim))

	createViaPortable(t, s, "rl-secret", "val")

	_, err := s.GetSecret(context.Background(), "rl-secret")
	assertError(t, err, true)
}

func TestWithLatency(t *testing.T) {
	s := newTestSecrets(WithLatency(time.Millisecond))

	start := time.Now()

	createViaPortable(t, s, "lat-secret", "val")

	elapsed := time.Since(start)
	if elapsed < time.Millisecond {
		t.Error("expected latency to be applied")
	}
}

func TestGetSecretValueErrorPath(t *testing.T) {
	s := newTestSecrets()

	_, err := s.GetSecretValue(context.Background(), "missing", "")

	assertError(t, err, true)
}

func TestWithRateLimiterAndRecorder(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	o := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	lim := ratelimit.New(1, 1, fc)
	rec := recorder.New()
	s := NewSecrets(secretsmanager.New(o), WithRateLimiter(lim), WithRecorder(rec))

	createViaPortable(t, s, "rlr-secret", "val")

	_, err := s.ListSecrets(context.Background())
	assertError(t, err, true)

	assertEqual(t, 2, rec.CallCount())
}
