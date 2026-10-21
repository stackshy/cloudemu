package cloudwatchlogs

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	accessOK   = `127.0.0.1 - frank [10/Oct/2000:13:25:15 -0700] "GET /index.html HTTP/1.0" 200 1534`
	accessMiss = `127.0.0.1 - frank [10/Oct/2000:13:35:22 -0700] "GET /about.html HTTP/1.0" 404 2000`
	accessGif  = `127.0.0.1 - frank [10/Oct/2000:13:50:35 -0700] "GET /logo.gif HTTP/1.0" 404 500`
)

// TestFilterLogEventsSpaceDelimited is the regression for space-delimited
// patterns being rejected.
func TestFilterLogEventsSpaceDelimited(t *testing.T) {
	m := newWindowMock(t, 0)
	putMessages(t, m, accessOK, accessMiss, accessGif)

	assert.Equal(t, []string{accessMiss}, filterMessages(t, m, `[..., request=*.html*, status_code=4*, bytes]`))
	assert.Equal(t, []string{accessMiss, accessGif}, filterMessages(t, m, `[ip, id, user, ts, request, status_code=404, bytes]`))
	assert.Equal(t, []string{accessOK, accessMiss}, filterMessages(t, m, `[..., bytes >= 1000]`))
}

// TestMetricFilterSpaceDelimitedValue publishes the sum of an extracted field,
// where the old code counted each match as 1.
func TestMetricFilterSpaceDelimitedValue(t *testing.T) {
	ctx := context.Background()
	m := newWindowMock(t, 0)
	mon := &recordingMonitoring{}
	m.SetMonitoring(mon)

	require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "bytes", LogGroup: "g", FilterPattern: `[ip, id, user, ts, request, status_code=404, bytes]`,
		MetricName: "Bytes404", MetricNamespace: "Web", MetricValue: "$bytes",
	}))
	require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name: "latency", LogGroup: "g", FilterPattern: `{ $.level = "ERROR" }`,
		MetricName: "Latency", MetricNamespace: "Web", MetricValue: "$.latency",
	}))

	putMessages(t, m, accessOK, accessMiss, accessGif, `{"level":"ERROR","latency":40}`, `{"level":"ERROR"}`)

	assert.InDelta(t, 2500.0, mon.valueOf("Web", "Bytes404"), 0)
	assert.InDelta(t, 40.0, mon.valueOf("Web", "Latency"), 0, "an event without the field contributes nothing")
}

func TestTestMetricFilterSpaceDelimited(t *testing.T) {
	got, err := newTestMock().TestMetricFilter(context.Background(), `[..., status_code=404, size]`,
		[]string{accessOK, accessMiss})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].EventNumber)
	assert.Equal(t, "404", got[0].ExtractedValues["$status_code"])
	assert.Equal(t, "2000", got[0].ExtractedValues["$size"])
	assert.Equal(t, "GET /about.html HTTP/1.0", got[0].ExtractedValues["$5"])
}
