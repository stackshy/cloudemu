package lambda

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestLambdaDurationUnitAndThrottles pins that Duration is published in
// Milliseconds (real Lambda's unit) and that an invoke throttled by reserved
// concurrency records the Throttles metric.
func TestLambdaDurationUnitAndThrottles(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	_, err := m.CreateFunction(ctx, defaultFuncConfig())
	requireNoError(t, err)
	m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte("{}")})
	requireNoError(t, err)

	query := func(name string) *mondriver.MetricDataResult {
		t.Helper()

		res, qerr := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/Lambda", MetricName: name,
			Dimensions: map[string]string{"FunctionName": "my-func"},
			StartTime:  fc.Now().Add(-time.Minute), EndTime: fc.Now().Add(time.Minute),
			Period: 60, Stat: "Sum",
		})
		requireNoError(t, qerr)

		return res
	}

	dur := query("Duration")
	assertEqual(t, 1, len(dur.Values))
	assertEqual(t, "Milliseconds", dur.Unit)
	assertEqual(t, "Count", query("Invocations").Unit)

	requireNoError(t, m.PutFunctionConcurrency(ctx, driver.ConcurrencyConfig{
		FunctionName: "my-func", ReservedConcurrentExecutions: 0,
	}))

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte("{}")})
	assertError(t, err, true)

	thr := query("Throttles")
	if len(thr.Values) != 1 || thr.Values[0] != 1 {
		t.Fatalf("Throttles = %v, want [1]", thr.Values)
	}

	assertEqual(t, "Count", thr.Unit)
}

// TestLambdaDurationIsMeasured pins that Duration is the handler's measured run
// time on the configured clock (here a handler that takes 250ms of FakeClock
// time), not a constant — for successful and failed invocations alike.
func TestLambdaDurationIsMeasured(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	m := New(opts)
	cw := cloudwatch.New(opts)
	m.SetMonitoring(cw)

	ctx := context.Background()

	_, err := m.CreateFunction(ctx, defaultFuncConfig())
	requireNoError(t, err)

	fail := false

	m.RegisterHandler("my-func", func(_ context.Context, _ []byte) ([]byte, error) {
		fc.Advance(250 * time.Millisecond)

		if fail {
			return nil, errTestHandler
		}

		return []byte("ok"), nil
	})

	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte("{}")})
	requireNoError(t, err)

	fail = true
	_, err = m.Invoke(ctx, driver.InvokeInput{FunctionName: "my-func", Payload: []byte("{}")})
	requireNoError(t, err)

	for stat, want := range map[string]float64{"Maximum": 250, "Minimum": 250, "SampleCount": 2} {
		res, qerr := cw.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/Lambda", MetricName: "Duration",
			Dimensions: map[string]string{"FunctionName": "my-func"},
			StartTime:  fc.Now().Add(-time.Minute), EndTime: fc.Now().Add(time.Minute),
			Period: 120, Stat: stat,
		})
		requireNoError(t, qerr)

		if len(res.Values) != 1 || res.Values[0] != want {
			t.Errorf("Duration %s = %v, want %v", stat, res.Values, want)
		}
	}
}

var errTestHandler = errors.New("handler failed")
