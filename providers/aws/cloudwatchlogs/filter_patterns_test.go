package cloudwatchlogs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingMonitoring captures PutMetricData calls; every other Monitoring
// method is left to the nil embedded interface and must not be called.
type recordingMonitoring struct {
	mondriver.Monitoring

	mu   sync.Mutex
	data []mondriver.MetricDatum
}

func (r *recordingMonitoring) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data = append(r.data, data...)

	return nil
}

func (r *recordingMonitoring) valueOf(namespace, name string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	var total float64

	for _, d := range r.data {
		if d.Namespace == namespace && d.MetricName == name {
			total += d.Value
		}
	}

	return total
}

func putMessages(t *testing.T, m *Mock, msgs ...string) {
	t.Helper()

	base := testEventTime()
	events := make([]driver.LogEvent, 0, len(msgs))

	for i, msg := range msgs {
		events = append(events, driver.LogEvent{Timestamp: base.Add(time.Duration(i) * time.Second), Message: msg})
	}

	_, err := m.PutLogEventsWithResult(context.Background(), "g", "s", events)
	require.NoError(t, err)
}

func filterMessages(t *testing.T, m *Mock, pattern string) []string {
	t.Helper()

	got, err := m.FilterLogEvents(context.Background(), &driver.FilterLogEventsInput{LogGroup: "g", FilterPattern: pattern})
	require.NoError(t, err)

	out := make([]string, 0, len(got))
	for _, e := range got {
		out = append(out, e.Message)
	}

	return out
}

// TestFilterLogEventsPatternLanguage is the regression for FilterLogEvents
// treating the whole pattern as one substring.
func TestFilterLogEventsPatternLanguage(t *testing.T) {
	m := newWindowMock(t, 0)
	putMessages(t, m,
		"[ERROR 419] MISSING ARGUMENTS",
		"[ERROR 400] BAD REQUEST",
		`{"level":"ERROR","latency":50}`,
		`{"level":"INFO","latency":5}`,
	)

	assert.Equal(t, []string{"[ERROR 400] BAD REQUEST"}, filterMessages(t, m, "ERROR -ARGUMENTS -level"))
	assert.Equal(t, []string{"[ERROR 419] MISSING ARGUMENTS"}, filterMessages(t, m, `"MISSING ARGUMENTS"`))
	assert.Len(t, filterMessages(t, m, "?BAD ?MISSING"), 2)
	assert.Equal(t, []string{`{"level":"ERROR","latency":50}`}, filterMessages(t, m, `{ $.level = "ERROR" && $.latency > 10 }`))
	assert.Len(t, filterMessages(t, m, "%ERR.R%"), 3)

	_, err := m.FilterLogEvents(context.Background(), &driver.FilterLogEventsInput{LogGroup: "g", FilterPattern: "{ $.a IS NOT NULL }"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestPutFilterValidatesPattern(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)

	err := m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "bad", LogGroup: "g", FilterPattern: "{ $.a EXISTS }", MetricName: "M", MetricNamespace: "N",
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	err = m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "bad", LogGroup: "g", FilterPattern: "%(x)%", DestinationARN: lambdaDestARN,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}

func TestRegexFilterQuotaPerGroup(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)

	for i := range 4 {
		require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
			Name: fmt.Sprintf("mf%d", i), LogGroup: "g", FilterPattern: "%ERR%", MetricName: "M", MetricNamespace: "N",
		}))
	}

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "sf", LogGroup: "g", FilterPattern: "%ERR%", DestinationARN: lambdaDestARN,
	}))

	// A sixth regex pattern, from either kind of filter, is over the quota.
	err := m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "mf9", LogGroup: "g", FilterPattern: "%WARN%", MetricName: "M", MetricNamespace: "N",
	})
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))

	// Replacing an existing regex filter, or adding a plain one, is fine.
	require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "mf0", LogGroup: "g", FilterPattern: "%WARN%", MetricName: "M", MetricNamespace: "N",
	}))
	require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "plain", LogGroup: "g", FilterPattern: "WARN", MetricName: "M", MetricNamespace: "N",
	}))
}

// TestSubscriptionFilterUsesPatternLanguage delivers only events a JSON
// pattern matches, where substring matching would have delivered none.
func TestSubscriptionFilterUsesPatternLanguage(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	inv := &recordingInvoker{}
	m.SetLambdaInvoker(inv)

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "errors", LogGroup: "g", FilterPattern: `{ $.level = "ERROR" }`, DestinationARN: lambdaDestARN,
	}))

	putMessages(t, m, `{"level":"ERROR","msg":"boom"}`, `{"level":"INFO"}`)

	inv.mu.Lock()
	defer inv.mu.Unlock()

	require.Len(t, inv.payloads, 1)

	ev := decodeAWSLogs(t, inv.payloads[0])
	require.Len(t, ev.LogEvents, 1)
	assert.Equal(t, `{"level":"ERROR","msg":"boom"}`, ev.LogEvents[0].Message)
}

// TestMetricFilterUsesPatternLanguage counts events with a ?OR pattern, which
// substring matching never matched.
func TestMetricFilterUsesPatternLanguage(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	mon := &recordingMonitoring{}
	m.SetMonitoring(mon)

	require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "errs", LogGroup: "g", FilterPattern: "?ERROR ?FATAL", MetricName: "Errors", MetricNamespace: "App",
	}))

	putMessages(t, m, "ERROR a", "FATAL b", "INFO c")

	assert.InDelta(t, 2.0, mon.valueOf("App", "Errors"), 0)
}

func TestTestMetricFilter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	got, err := m.TestMetricFilter(ctx, `{ $.latency > 10 }`, []string{`{"latency":5}`, `{"latency":50}`, "plain"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].EventNumber)
	assert.Equal(t, map[string]string{"$.latency": "50"}, got[0].ExtractedValues)

	got, err = m.TestMetricFilter(ctx, `"[ERROR]" Exception`, []string{"[INFO] ok", "[ERROR] InvalidQueryException", "[ERROR] stop"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].EventNumber)
	assert.Empty(t, got[0].ExtractedValues)

	for _, bad := range [][]string{nil, {""}} {
		_, err = m.TestMetricFilter(ctx, "x", bad)
		require.Error(t, err)
		assert.True(t, errors.IsInvalidArgument(err))
	}

	_, err = m.TestMetricFilter(ctx, "{ $.a IS NOT NULL }", []string{"x"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))
}
