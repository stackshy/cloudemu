package cloudwatchlogs

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newInsightsMock(t *testing.T) *Mock {
	t.Helper()

	m := newWindowMock(t, 0)
	putMessages(t, m,
		`{"level":"ERROR","latency":120,"user":{"id":"u1"}}`,
		`{"level":"INFO","latency":15}`,
		"plain ERROR Exception text",
	)

	return m
}

func startQuery(t *testing.T, m *Mock, q string) string {
	t.Helper()

	now := m.opts.Clock.Now()

	id, err := m.StartQuery(context.Background(), &driver.StartQueryInput{
		LogGroupName: "g", StartTime: now.Add(-6 * time.Hour), EndTime: now, QueryString: q,
	})
	require.NoError(t, err)

	return id
}

func rowField(row []driver.QueryResultField, name string) string {
	for _, f := range row {
		if f.Field == name {
			return f.Value
		}
	}

	return ""
}

func TestInsightsQueryLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newInsightsMock(t)

	id := startQuery(t, m, "fields @timestamp, level, latency | filter latency > 10 | sort latency desc | limit 1")

	infos, err := m.DescribeQueries(ctx, driver.DescribeQueriesInput{Status: driver.QueryStatusRunning})
	require.NoError(t, err)
	require.Len(t, infos, 1, "a query is Running until its first poll")

	res, err := m.GetQueryResults(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, driver.QueryStatusComplete, res.Status)
	assert.Equal(t, "CWLI", res.QueryLanguage)
	require.Len(t, res.Results, 1)
	assert.Equal(t, "120", rowField(res.Results[0], "latency"))
	assert.Equal(t, "ERROR", rowField(res.Results[0], "level"))
	assert.NotEmpty(t, rowField(res.Results[0], "@ptr"))
	assert.InDelta(t, 2.0, res.Statistics.RecordsMatched, 0)
	assert.InDelta(t, 3.0, res.Statistics.RecordsScanned, 0)
	assert.Positive(t, res.Statistics.BytesScanned)

	// A completed query can no longer be stopped.
	err = m.StopQuery(ctx, id)
	require.Error(t, err)
	assert.True(t, errors.IsInvalidArgument(err))

	rec, err := m.GetLogRecord(ctx, rowField(res.Results[0], "@ptr"))
	require.NoError(t, err)
	assert.Equal(t, `{"level":"ERROR","latency":120,"user":{"id":"u1"}}`, rec["@message"])
	assert.Equal(t, "u1", rec["user.id"])
	assert.Equal(t, "s", rec["@logStream"])
	assert.Equal(t, "123456789012:g", rec["@log"])
	assert.NotContains(t, rec, "@ptr")

	done, err := m.DescribeQueries(ctx, driver.DescribeQueriesInput{LogGroupName: "g", Status: driver.QueryStatusComplete})
	require.NoError(t, err)
	require.Len(t, done, 1)
	assert.Equal(t, "g", done[0].LogGroupName)
}

func TestInsightsStopAndTimeout(t *testing.T) {
	ctx := context.Background()
	m := newInsightsMock(t)

	stopped := startQuery(t, m, "fields @message")
	require.NoError(t, m.StopQuery(ctx, stopped))

	res, err := m.GetQueryResults(ctx, stopped)
	require.NoError(t, err)
	assert.Equal(t, driver.QueryStatusCancelled, res.Status)
	assert.Empty(t, res.Results)

	slow := startQuery(t, m, "fields @message")
	advanceClock(t, m, insightsQueryTimeout+time.Minute)

	res, err = m.GetQueryResults(ctx, slow)
	require.NoError(t, err)
	assert.Equal(t, driver.QueryStatusTimeout, res.Status)

	err = m.StopQuery(ctx, "nope")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))

	_, err = m.GetQueryResults(ctx, "nope")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

func TestInsightsStartQueryValidation(t *testing.T) {
	ctx := context.Background()
	m := newInsightsMock(t)
	now := m.opts.Clock.Now()

	base := func() *driver.StartQueryInput {
		return &driver.StartQueryInput{LogGroupName: "g", StartTime: now.Add(-time.Hour), EndTime: now, QueryString: "fields @message"}
	}

	cases := map[string]func(in *driver.StartQueryInput){
		"no log group":       func(in *driver.StartQueryInput) { in.LogGroupName = "" },
		"two selectors":      func(in *driver.StartQueryInput) { in.LogGroupNames = []string{"g"} },
		"end before start":   func(in *driver.StartQueryInput) { in.EndTime = in.StartTime.Add(-time.Second) },
		"limit too large":    func(in *driver.StartQueryInput) { in.Limit = maxQueryLimit + 1 },
		"sql not supported":  func(in *driver.StartQueryInput) { in.QueryLanguage = "SQL" },
		"too many log group": func(in *driver.StartQueryInput) { in.LogGroupName, in.LogGroupNames = "", make([]string, 51) },
	}

	for name, mutate := range cases {
		in := base()
		mutate(in)

		_, err := m.StartQuery(ctx, in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalidArgument(err), "%s: %v", name, err)
	}

	in := base()
	in.LogGroupName = "missing"
	_, err := m.StartQuery(ctx, in)
	assert.True(t, errors.IsNotFound(err))

	in = base()
	in.LogGroupName, in.LogGroupIdentifiers = "", []string{"arn:aws:logs:us-east-1:123456789012:log-group:g:*"}
	_, err = m.StartQuery(ctx, in)
	require.NoError(t, err, "a log group ARN identifies the group")

	in = base()
	in.QueryString = "fields @message | stats count(*)"
	_, err = m.StartQuery(ctx, in)

	var mq *driver.MalformedQueryError
	require.True(t, stderrors.As(err, &mq), "got %v", err)
	assert.Equal(t, len("fields @message | "), mq.Start)
}

func TestInsightsConcurrentQueryLimit(t *testing.T) {
	m := newInsightsMock(t)

	for range maxConcurrentInsightsQueries {
		startQuery(t, m, "fields @message")
	}

	now := m.opts.Clock.Now()

	_, err := m.StartQuery(context.Background(), &driver.StartQueryInput{
		LogGroupName: "g", StartTime: now.Add(-time.Hour), EndTime: now, QueryString: "fields @message",
	})
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))
}

func TestInsightsTimeRangeAndSnapshot(t *testing.T) {
	ctx := context.Background()
	m := newInsightsMock(t)
	ev := testEventTime() // putMessages starts here, one second apart

	// A range that ends on the first event's second includes it (inclusive).
	id, err := m.StartQuery(ctx, &driver.StartQueryInput{
		LogGroupName: "g", StartTime: ev, EndTime: ev, QueryString: "fields @message",
	})
	require.NoError(t, err)

	// A Running query survives a snapshot and runs on its first poll after.
	raw, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, raw))

	res, err := restored.GetQueryResults(ctx, id)
	require.NoError(t, err)
	require.Equal(t, driver.QueryStatusComplete, res.Status)
	require.Len(t, res.Results, 1)
	assert.Equal(t, `{"level":"ERROR","latency":120,"user":{"id":"u1"}}`, rowField(res.Results[0], "@message"))
}

func TestGetLogGroupFields(t *testing.T) {
	ctx := context.Background()
	m := newInsightsMock(t)

	fields, err := m.GetLogGroupFields(ctx, "g", testEventTime())
	require.NoError(t, err)

	got := map[string]int{}
	for _, f := range fields {
		got[f.Name] = f.Percent
	}

	assert.Equal(t, 100, got["@message"])
	assert.Equal(t, 66, got["level"])
	assert.Equal(t, 33, got["user.id"])
	assert.Equal(t, 100, fields[0].Percent)

	// Without a time it samples the last 15 minutes; the events are 3h old.
	recent, err := m.GetLogGroupFields(ctx, "g", time.Time{})
	require.NoError(t, err)
	assert.Empty(t, recent)

	_, err = m.GetLogGroupFields(ctx, "nope", time.Time{})
	assert.True(t, errors.IsNotFound(err))

	_, err = m.GetLogRecord(ctx, "not-a-pointer")
	assert.True(t, errors.IsInvalidArgument(err))
}
