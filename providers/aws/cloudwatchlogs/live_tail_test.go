package cloudwatchlogs

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const liveTailTestARN = "arn:aws:logs:us-east-1:123456789012:log-group:"

// liveTailWait bounds how long a test waits for the next one-second update.
const liveTailWait = 5 * time.Second

func newLiveTailMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(testClockStart())
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1")))

	ctx := context.Background()

	for _, g := range []string{"lt-a", "lt-b"} {
		_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: g})
		require.NoError(t, err)

		for _, s := range []string{"app-1", "other"} {
			_, err = m.CreateLogStream(ctx, g, s)
			require.NoError(t, err)
		}
	}

	return m, fc
}

// nextResults returns the first update that carries events.
func nextResults(t *testing.T, s driver.LiveTailSession) driver.LiveTailUpdate {
	t.Helper()

	deadline := time.After(liveTailWait)

	for {
		select {
		case u, ok := <-s.Updates():
			require.True(t, ok, "session ended before any results")

			if len(u.Results) > 0 {
				return u
			}
		case <-deadline:
			t.Fatal("no Live Tail update with results")
		}
	}
}

// waitEnded waits for the session's update channel to close.
func waitEnded(t *testing.T, s driver.LiveTailSession) {
	t.Helper()

	deadline := time.After(liveTailWait)

	for {
		select {
		case _, ok := <-s.Updates():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("Live Tail session did not end")
		}
	}
}

func TestStartLiveTailValidation(t *testing.T) {
	m, _ := newLiveTailMock(t)
	arn := liveTailTestARN + "lt-a"

	eleven := make([]string, maxLiveTailLogGroups+1)
	for i := range eleven {
		eleven[i] = arn
	}

	tests := []struct {
		name string
		in   driver.StartLiveTailInput
		code errors.Code
	}{
		{"no groups", driver.StartLiveTailInput{}, errors.InvalidArgument},
		{"eleven groups", driver.StartLiveTailInput{LogGroupIdentifiers: eleven}, errors.InvalidArgument},
		{"name not ARN", driver.StartLiveTailInput{LogGroupIdentifiers: []string{"lt-a"}}, errors.InvalidArgument},
		{"ARN ends with asterisk", driver.StartLiveTailInput{LogGroupIdentifiers: []string{arn + ":*"}}, errors.InvalidArgument},
		{"names and prefixes", driver.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn}, LogStreamNames: []string{"a"}, LogStreamNamePrefixes: []string{"a"},
		}, errors.InvalidArgument},
		{"streams with two groups", driver.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn, liveTailTestARN + "lt-b"}, LogStreamNames: []string{"a"},
		}, errors.InvalidArgument},
		{"bad stream name", driver.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn}, LogStreamNamePrefixes: []string{"a:b"},
		}, errors.InvalidArgument},
		{"pattern too long", driver.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn}, LogEventFilterPattern: strings.Repeat("a", maxLiveTailPatternLen+1),
		}, errors.InvalidArgument},
		{"bad pattern", driver.StartLiveTailInput{
			LogGroupIdentifiers: []string{arn}, LogEventFilterPattern: "{ $.a = ",
		}, errors.InvalidArgument},
		{"unknown group", driver.StartLiveTailInput{LogGroupIdentifiers: []string{liveTailTestARN + "nope"}}, errors.NotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.StartLiveTail(context.Background(), &tt.in)
			require.Error(t, err)
			assert.Equal(t, tt.code, errors.GetCode(err), err.Error())
		})
	}
}

func TestLiveTailDeliversMatchingEvents(t *testing.T) {
	m, fc := newLiveTailMock(t)
	ctx := context.Background()

	sess, err := m.StartLiveTail(ctx, &driver.StartLiveTailInput{
		LogGroupIdentifiers:   []string{liveTailTestARN + "lt-a"},
		LogStreamNamePrefixes: []string{"app"},
		LogEventFilterPattern: "ERROR",
	})
	require.NoError(t, err)

	defer sess.Close()

	ts := fc.Now()
	for _, target := range []struct{ group, stream, msg string }{
		{"lt-a", "app-1", "ERROR disk full"},
		{"lt-a", "app-1", "INFO all good"},
		{"lt-a", "other", "ERROR other stream"},
		{"lt-b", "app-1", "ERROR other group"},
	} {
		require.NoError(t, m.PutLogEvents(ctx, target.group, target.stream,
			[]driver.LogEvent{{Timestamp: ts, Message: target.msg}}))
	}

	u := nextResults(t, sess)
	require.Len(t, u.Results, 1)

	got := u.Results[0]
	assert.Equal(t, liveTailTestARN+"lt-a", got.LogGroupIdentifier)
	assert.Equal(t, "app-1", got.LogStreamName)
	assert.Equal(t, "ERROR disk full", got.Message)
	assert.Equal(t, ts, got.Timestamp)
	assert.False(t, got.IngestionTime.IsZero())
	assert.False(t, u.Sampled)
}

func TestLiveTailStreamNamesAndMasking(t *testing.T) {
	m, fc := newLiveTailMock(t)
	ctx := context.Background()

	_, err := m.PutDataProtectionPolicy(ctx, "lt-a", dpPolicy("EmailAddress"))
	require.NoError(t, err)

	sess, err := m.StartLiveTail(ctx, &driver.StartLiveTailInput{
		LogGroupIdentifiers: []string{liveTailTestARN + "lt-a"},
		LogStreamNames:      []string{"other"},
	})
	require.NoError(t, err)

	defer sess.Close()

	require.NoError(t, m.PutLogEvents(ctx, "lt-a", "app-1", []driver.LogEvent{{Timestamp: fc.Now(), Message: "skip"}}))
	require.NoError(t, m.PutLogEvents(ctx, "lt-a", "other", []driver.LogEvent{{Timestamp: fc.Now(), Message: "mail a@b.io"}}))

	u := nextResults(t, sess)
	require.Len(t, u.Results, 1)
	assert.Equal(t, "other", u.Results[0].LogStreamName)
	assert.Equal(t, "mail ******", u.Results[0].Message)
}

func TestLiveTailSamplesAndBuffers(t *testing.T) {
	m, _ := newLiveTailMock(t)

	tail, err := m.newLiveTail(&driver.StartLiveTailInput{LogGroupIdentifiers: []string{liveTailTestARN + "lt-a"}})
	require.NoError(t, err)

	events := make([]driver.LogEvent, liveTailBufferedEvents+100)
	for i := range events {
		events[i] = driver.LogEvent{Message: fmt.Sprintf("m%d", i)}
	}

	tail.offer("lt-a", "s", events)

	u := tail.drain()
	assert.True(t, u.Sampled)
	assert.Len(t, u.Results, liveTailMaxEventsPerUpdate)
	assert.Equal(t, "m100", u.Results[0].Message, "the oldest events beyond the buffer are dropped")

	empty := tail.drain()
	assert.False(t, empty.Sampled)
	assert.NotNil(t, empty.Results)
	assert.Empty(t, empty.Results)

	for i := range liveTailBufferedUpdates + 1 {
		tail.send(driver.LiveTailUpdate{Results: []driver.LiveTailEvent{{Message: fmt.Sprint(i)}}})
	}

	first := <-tail.out
	assert.Equal(t, "1", first.Results[0].Message, "the oldest queued update is dropped")
	assert.Len(t, tail.out, liveTailBufferedUpdates-1)
}

func TestLiveTailEndsAndReleases(t *testing.T) {
	m, fc := newLiveTailMock(t)
	in := &driver.StartLiveTailInput{LogGroupIdentifiers: []string{liveTailTestARN + "lt-a"}}
	before := runtime.NumGoroutine()

	// Timeout after three hours on the mock clock.
	timed, err := m.StartLiveTail(context.Background(), in)
	require.NoError(t, err)
	fc.Advance(liveTailSessionTimeout)
	waitEnded(t, timed)
	assert.True(t, timed.TimedOut())

	// Context cancellation (client disconnect) and Close.
	ctx, cancel := context.WithCancel(context.Background())
	byCtx, err := m.StartLiveTail(ctx, in)
	require.NoError(t, err)

	closed, err := m.StartLiveTail(context.Background(), in)
	require.NoError(t, err)

	cancel()
	closed.Close()
	closed.Close()
	waitEnded(t, byCtx)
	waitEnded(t, closed)
	assert.False(t, byCtx.TimedOut())
	assert.False(t, closed.TimedOut())

	// Polled by hand: require.Eventually runs its condition on a goroutine of
	// its own, which would skew the count.
	deadline := time.Now().Add(liveTailWait)
	for time.Now().Before(deadline) {
		m.liveTailMu.Lock()
		n := len(m.liveTails)
		m.liveTailMu.Unlock()

		if n == 0 && runtime.NumGoroutine() <= before {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("sessions or goroutines left: goroutines %d, before %d", runtime.NumGoroutine(), before)
}

func TestLiveTailConcurrentSessionLimit(t *testing.T) {
	m, _ := newLiveTailMock(t)
	in := &driver.StartLiveTailInput{LogGroupIdentifiers: []string{liveTailTestARN + "lt-a"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for range maxLiveTailSessions {
		_, err := m.StartLiveTail(ctx, in)
		require.NoError(t, err)
	}

	_, err := m.StartLiveTail(ctx, in)
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))
}
