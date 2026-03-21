package eventbus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/eventbus/driver"
	"github.com/stackshy/cloudemu/inject"
	"github.com/stackshy/cloudemu/metrics"
	"github.com/stackshy/cloudemu/providers/aws/eventbridge"
	"github.com/stackshy/cloudemu/ratelimit"
	"github.com/stackshy/cloudemu/recorder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDriver() (driver.EventBus, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return eventbridge.New(opts), fc
}

func newTestEventBus(opts ...Option) (*EventBus, *config.FakeClock) {
	d, fc := newTestDriver()
	return NewEventBus(d, opts...), fc
}

func TestNewEventBus(t *testing.T) {
	eb, _ := newTestEventBus()

	require.NotNil(t, eb)
	require.NotNil(t, eb.driver)
}

func TestCreateEventBusPortable(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		info, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "my-bus"})
		require.NoError(t, err)
		assert.Equal(t, "my-bus", info.Name)
	})

	t.Run("empty name error", func(t *testing.T) {
		_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{})
		require.Error(t, err)
	})
}

func TestDeleteEventBusPortable(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "del-bus"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		delErr := eb.DeleteEventBus(ctx, "del-bus")
		require.NoError(t, delErr)
	})

	t.Run("not found", func(t *testing.T) {
		delErr := eb.DeleteEventBus(ctx, "nonexistent")
		require.Error(t, delErr)
	})
}

func TestGetEventBusPortable(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "get-bus"})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		info, getErr := eb.GetEventBus(ctx, "get-bus")
		require.NoError(t, getErr)
		assert.Equal(t, "get-bus", info.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, getErr := eb.GetEventBus(ctx, "nonexistent")
		require.Error(t, getErr)
	})
}

func TestListEventBusesPortable(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	buses, err := eb.ListEventBuses(ctx)
	require.NoError(t, err)
	initialCount := len(buses)

	_, err = eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "a"})
	require.NoError(t, err)

	_, err = eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "b"})
	require.NoError(t, err)

	buses, err = eb.ListEventBuses(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialCount+2, len(buses))
}

func TestPutRulePortable(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "rule-bus"})
	require.NoError(t, err)

	rule, err := eb.PutRule(ctx, &driver.RuleConfig{
		Name:         "my-rule",
		EventBus:     "rule-bus",
		EventPattern: `{"source": ["my.app"]}`,
		State:        "ENABLED",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-rule", rule.Name)
}

func TestPutEventsPortable(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "evt-bus"})
	require.NoError(t, err)

	result, err := eb.PutEvents(ctx, []driver.Event{
		{
			Source:     "my.app",
			DetailType: "MyEvent",
			Detail:     `{"key": "value"}`,
			EventBus:   "evt-bus",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.SuccessCount)
}

func TestWithRecorder(t *testing.T) {
	rec := recorder.New()
	eb, _ := newTestEventBus(WithRecorder(rec))
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "rec-bus"})
	require.NoError(t, err)

	_, err = eb.GetEventBus(ctx, "rec-bus")
	require.NoError(t, err)

	_, err = eb.ListEventBuses(ctx)
	require.NoError(t, err)

	totalCalls := rec.CallCount()
	assert.GreaterOrEqual(t, totalCalls, 3)

	createCalls := rec.CallCountFor("eventbus", "CreateEventBus")
	assert.Equal(t, 1, createCalls)

	getCalls := rec.CallCountFor("eventbus", "GetEventBus")
	assert.Equal(t, 1, getCalls)

	listCalls := rec.CallCountFor("eventbus", "ListEventBuses")
	assert.Equal(t, 1, listCalls)
}

func TestWithRecorderOnError(t *testing.T) {
	rec := recorder.New()
	eb, _ := newTestEventBus(WithRecorder(rec))
	ctx := context.Background()

	_, _ = eb.GetEventBus(ctx, "nonexistent")

	totalCalls := rec.CallCount()
	assert.Equal(t, 1, totalCalls)

	last := rec.LastCall()
	require.NotNil(t, last, "expected a recorded call")
	assert.NotNil(t, last.Error, "expected recorded call to have an error")
}

func TestWithMetrics(t *testing.T) {
	mc := metrics.NewCollector()
	eb, _ := newTestEventBus(WithMetrics(mc))
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "met-bus"})
	require.NoError(t, err)

	_, err = eb.GetEventBus(ctx, "met-bus")
	require.NoError(t, err)

	_, err = eb.ListEventBuses(ctx)
	require.NoError(t, err)

	q := metrics.NewQuery(mc)

	callsCount := q.ByName("calls_total").Count()
	assert.GreaterOrEqual(t, callsCount, 3)

	durCount := q.ByName("call_duration").Count()
	assert.GreaterOrEqual(t, durCount, 3)
}

func TestWithMetricsOnError(t *testing.T) {
	mc := metrics.NewCollector()
	eb, _ := newTestEventBus(WithMetrics(mc))
	ctx := context.Background()

	_, _ = eb.GetEventBus(ctx, "nonexistent")

	q := metrics.NewQuery(mc)

	errCount := q.ByName("errors_total").Count()
	assert.Equal(t, 1, errCount)
}

func TestWithErrorInjection(t *testing.T) {
	inj := inject.NewInjector()
	eb, _ := newTestEventBus(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("injected failure")
	inj.Set("eventbus", "CreateEventBus", injectedErr, inject.Always{})

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "fail-bus"})
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)
}

func TestWithErrorInjectionRecorded(t *testing.T) {
	rec := recorder.New()
	inj := inject.NewInjector()
	eb, _ := newTestEventBus(WithErrorInjection(inj), WithRecorder(rec))
	ctx := context.Background()

	injectedErr := fmt.Errorf("boom")
	inj.Set("eventbus", "GetEventBus", injectedErr, inject.Always{})

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "inj-bus"})
	require.NoError(t, err)

	_, err = eb.GetEventBus(ctx, "inj-bus")
	require.Error(t, err)

	getCalls := rec.CallsFor("eventbus", "GetEventBus")
	assert.Equal(t, 1, len(getCalls))
	assert.NotNil(t, getCalls[0].Error, "expected recorded GetEventBus call to have an error")
}

func TestWithErrorInjectionRemoved(t *testing.T) {
	inj := inject.NewInjector()
	eb, _ := newTestEventBus(WithErrorInjection(inj))
	ctx := context.Background()

	injectedErr := fmt.Errorf("fail")
	inj.Set("eventbus", "CreateEventBus", injectedErr, inject.Always{})

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "test"})
	require.Error(t, err)

	inj.Remove("eventbus", "CreateEventBus")

	_, err = eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "test"})
	require.NoError(t, err)
}

func TestWithRateLimiter(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	d := eventbridge.New(opts)
	limiter := ratelimit.New(1, 1, fc)
	eb := NewEventBus(d, WithRateLimiter(limiter))
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "rl-bus"})
	require.NoError(t, err)

	_, err = eb.GetEventBus(ctx, "rl-bus")
	require.Error(t, err, "expected rate limit error on second call without time advance")
}

func TestWithLatency(t *testing.T) {
	latency := 1 * time.Millisecond
	eb, _ := newTestEventBus(WithLatency(latency))
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "lat-bus"})
	require.NoError(t, err)

	info, err := eb.GetEventBus(ctx, "lat-bus")
	require.NoError(t, err)
	assert.Equal(t, "lat-bus", info.Name)
}

func TestAllOptionsComposed(t *testing.T) {
	rec := recorder.New()
	mc := metrics.NewCollector()
	inj := inject.NewInjector()
	latency := 1 * time.Millisecond

	eb, _ := newTestEventBus(
		WithRecorder(rec),
		WithMetrics(mc),
		WithErrorInjection(inj),
		WithLatency(latency),
	)
	ctx := context.Background()

	_, err := eb.CreateEventBus(ctx, driver.EventBusConfig{Name: "all-opts"})
	require.NoError(t, err)

	_, err = eb.GetEventBus(ctx, "all-opts")
	require.NoError(t, err)

	assert.Equal(t, 2, rec.CallCount())

	q := metrics.NewQuery(mc)
	assert.Equal(t, 2, q.ByName("calls_total").Count())
}

func TestPortableDeleteEventBusError(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	err := eb.DeleteEventBus(ctx, "no-bus")
	require.Error(t, err)
}

func TestPortableDeleteRuleError(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	err := eb.DeleteRule(ctx, "no-bus", "no-rule")
	require.Error(t, err)
}

func TestPortableGetRuleError(t *testing.T) {
	eb, _ := newTestEventBus()
	ctx := context.Background()

	_, err := eb.GetRule(ctx, "no-bus", "no-rule")
	require.Error(t, err)
}
