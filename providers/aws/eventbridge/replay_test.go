package eventbridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSQS records every body delivered to each queue ARN.
type recordingSQS struct {
	mu     sync.Mutex
	bodies map[string][]string
}

func (r *recordingSQS) DeliverExternal(_ context.Context, queueARN, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.bodies == nil {
		r.bodies = map[string][]string{}
	}

	r.bodies[queueARN] = append(r.bodies[queueARN], body)

	return nil
}

func (r *recordingSQS) got(queueARN string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]map[string]any, 0, len(r.bodies[queueARN]))

	for _, b := range r.bodies[queueARN] {
		var env map[string]any
		if json.Unmarshal([]byte(b), &env) == nil {
			out = append(out, env)
		}
	}

	return out
}

const (
	queueA = "arn:aws:sqs:us-east-1:123456789012:qa"
	queueB = "arn:aws:sqs:us-east-1:123456789012:qb"
)

// replayFixture builds a bus "b1" with two rules (ra -> queueA, rb -> queueB)
// matching source "s", an archive on it, and two archived events.
func replayFixture(t *testing.T) (*Mock, *recordingSQS, *driver.Archive, time.Time) {
	t.Helper()

	m, fc := newTestMock()
	sqs := &recordingSQS{}
	m.SetSQSDeliverer(sqs)
	ctx := context.Background()

	createTestBus(t, m, "b1")

	for rule, queue := range map[string]string{"ra": queueA, "rb": queueB} {
		_, err := m.PutRule(ctx, &driver.RuleConfig{Name: rule, EventBus: "b1", EventPattern: `{"source":["s"]}`})
		require.NoError(t, err)
		require.NoError(t, m.PutTargets(ctx, "b1", rule, []driver.Target{{ID: "q", ARN: queue}}))
	}

	a := createTestArchive(t, m, "arch", "b1", "")
	start := fc.Now()

	putTestEvent(t, m, "b1", "s")
	fc.Advance(time.Minute)
	putTestEvent(t, m, "b1", "s")
	fc.Advance(time.Minute)

	// Clear what the original puts delivered so tests see only the replay.
	sqs.bodies = nil

	return m, sqs, a, start
}

func replayConfig(name string, a *driver.Archive, start time.Time, filters ...string) *driver.ReplayConfig {
	return &driver.ReplayConfig{
		Name: name, EventSourceARN: a.ARN,
		EventStartTime: start, EventEndTime: start.Add(time.Hour),
		Destination: driver.ReplayDestination{ARN: busARN("b1"), FilterARNs: filters},
	}
}

func TestStartReplayDeliversToFilteredRules(t *testing.T) {
	m, sqs, a, start := replayFixture(t)
	ctx := context.Background()

	out, err := m.StartReplay(ctx, replayConfig("rp", a, start, m.ruleARN("b1", "ra")))
	require.NoError(t, err)
	assert.Equal(t, "STARTING", out.State)
	assert.Equal(t, "arn:aws:events:us-east-1:123456789012:replay/rp", out.ARN)

	got := sqs.got(queueA)
	require.Len(t, got, 2)
	assert.Equal(t, "rp", got[0]["replay-name"])
	assert.Empty(t, sqs.got(queueB), "a rule outside FilterArns must not receive replayed events")

	desc, err := m.DescribeReplay(ctx, "rp")
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", desc.State)
	assert.False(t, desc.ReplayEndTime.IsZero())
	assert.Equal(t, start.Add(time.Minute).UTC(), desc.EventLastReplayedTime)

	arch, err := m.DescribeArchive(ctx, "arch")
	require.NoError(t, err)
	assert.Equal(t, int64(2), arch.EventCount, "replayed events must not be archived again")

	history, err := m.GetEventHistory(ctx, "b1", 0)
	require.NoError(t, err)
	assert.Len(t, history, 2, "replayed events are not added to the bus history")
}

func TestStartReplayWithoutFiltersUsesAllRules(t *testing.T) {
	m, sqs, a, start := replayFixture(t)

	_, err := m.StartReplay(context.Background(), replayConfig("rp", a, start))
	require.NoError(t, err)
	assert.Len(t, sqs.got(queueA), 2)
	assert.Len(t, sqs.got(queueB), 2)
}

func TestStartReplayTimeWindow(t *testing.T) {
	m, sqs, a, start := replayFixture(t)

	cfg := replayConfig("rp", a, start.Add(30*time.Second))
	_, err := m.StartReplay(context.Background(), cfg)
	require.NoError(t, err)
	assert.Len(t, sqs.got(queueA), 1, "only the event inside the window is replayed")
}

func TestStartReplayErrors(t *testing.T) {
	m, _, a, start := replayFixture(t)
	ctx := context.Background()
	createTestBus(t, m, "b2")

	tests := []struct {
		name    string
		mutate  func(c *driver.ReplayConfig)
		check   func(error) bool
		message string
	}{
		{
			name:    "unknown destination bus",
			mutate:  func(c *driver.ReplayConfig) { c.Destination.ARN = busARN("ghost") },
			check:   cerrors.IsNotFound,
			message: "Event bus ghost does not exist.",
		},
		{
			name:    "unknown archive",
			mutate:  func(c *driver.ReplayConfig) { c.EventSourceARN = "arn:aws:events:us-east-1:123456789012:archive/nope" },
			check:   cerrors.IsInvalidArgument,
			message: "Parameter EventSourceArn is not valid. Reason: Archive nope does not exist.",
		},
		{
			name:    "cross bus",
			mutate:  func(c *driver.ReplayConfig) { c.Destination.ARN = busARN("b2") },
			check:   cerrors.IsInvalidArgument,
			message: "Parameter Destination.Arn is not valid. Reason: Cross event bus replay is not permitted.",
		},
		{
			name:    "start equals end",
			mutate:  func(c *driver.ReplayConfig) { c.EventEndTime = c.EventStartTime },
			check:   cerrors.IsInvalidArgument,
			message: "Parameter EventEndTime is not valid. Reason: EventStartTime must be before EventEndTime.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := replayConfig("rp-"+tc.name[:4], a, start)
			tc.mutate(cfg)

			_, err := m.StartReplay(ctx, cfg)
			require.True(t, tc.check(err), "got %v", err)
			assert.Equal(t, tc.message, cerrors.Message(err))
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		_, err := m.StartReplay(ctx, replayConfig("dup", a, start))
		require.NoError(t, err)

		_, err = m.StartReplay(ctx, replayConfig("dup", a, start))
		require.True(t, cerrors.IsAlreadyExists(err))
		assert.Equal(t, "Replay dup already exists.", cerrors.Message(err))
	})
}

func TestCancelReplay(t *testing.T) {
	m, _, a, start := replayFixture(t)
	ctx := context.Background()

	_, err := m.CancelReplay(ctx, "ghost")
	require.True(t, cerrors.IsNotFound(err))
	assert.Equal(t, "Replay ghost does not exist.", cerrors.Message(err))

	_, err = m.StartReplay(ctx, replayConfig("rp", a, start))
	require.NoError(t, err)

	_, err = m.CancelReplay(ctx, "rp")
	require.True(t, errors.Is(err, driver.ErrIllegalStatus))
	assert.Equal(t, "Replay rp is not in a valid state for this operation.", cerrors.Message(err))

	// A replay still in progress can be cancelled and ends CANCELLED.
	m.replays.Set("live", driver.Replay{Name: "live", State: "RUNNING"})

	out, err := m.CancelReplay(ctx, "live")
	require.NoError(t, err)
	assert.Equal(t, "CANCELLING", out.State)
}

func TestReplayStopsWhenCancelled(t *testing.T) {
	m, sqs, a, start := replayFixture(t)

	replay := driver.Replay{
		Name: "rp", State: "CANCELLING", Destination: driver.ReplayDestination{ARN: busARN("b1")},
		EventStartTime: start, EventEndTime: start.Add(time.Hour),
	}
	m.replays.Set("rp", replay)

	ad, ok := m.archives.Get(a.Name)
	require.True(t, ok)

	m.runReplay(context.Background(), &replay, ad)

	assert.Empty(t, sqs.got(queueA))

	got, err := m.DescribeReplay(context.Background(), "rp")
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", got.State)
}

func TestReplayLimit(t *testing.T) {
	m, _, a, start := replayFixture(t)
	m.activeReplays.Store(maxActiveReplays)

	_, err := m.StartReplay(context.Background(), replayConfig("rp", a, start))
	require.Equal(t, cerrors.ResourceExhausted, cerrors.GetCode(err))
	assert.Equal(t, "The requested resource exceeds the maximum number allowed.", cerrors.Message(err))

	_, err = m.DescribeReplay(context.Background(), "rp")
	assert.True(t, cerrors.IsNotFound(err), "a rejected replay must not be recorded")
}

func TestListReplays(t *testing.T) {
	m, _, a, start := replayFixture(t)
	ctx := context.Background()

	for _, name := range []string{"rb", "ra"} {
		_, err := m.StartReplay(ctx, replayConfig(name, a, start))
		require.NoError(t, err)
	}

	all, err := m.ListReplays(ctx, driver.ReplayFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "ra", all[0].Name)

	byState, err := m.ListReplays(ctx, driver.ReplayFilter{State: "COMPLETED"})
	require.NoError(t, err)
	assert.Len(t, byState, 2)

	bySource, err := m.ListReplays(ctx, driver.ReplayFilter{EventSourceARN: a.ARN})
	require.NoError(t, err)
	assert.Len(t, bySource, 2)

	_, err = m.ListReplays(ctx, driver.ReplayFilter{NamePrefix: "r", EventSourceARN: a.ARN})
	require.True(t, cerrors.IsInvalidArgument(err))
	assert.Equal(t, "At most one filter is allowed for ListReplays. Use either : State, EventSourceArn, or NamePrefix.", cerrors.Message(err))
}

// TestArchiveAndReplayConcurrent exercises capture, update, list and replay at
// the same time so the race detector can check the locking.
func TestArchiveAndReplayConcurrent(t *testing.T) {
	m, _, a, start := replayFixture(t)
	ctx := context.Background()

	var wg sync.WaitGroup

	for _, name := range []string{"rp-a", "rp-b", "rp-c", "rp-d"} {
		wg.Add(3)

		go func() {
			defer wg.Done()

			_, _ = m.PutEvents(ctx, []driver.Event{{Source: "s", DetailType: "t", Detail: "{}", EventBus: "b1"}})
		}()

		go func() {
			defer wg.Done()

			_, _ = m.StartReplay(ctx, replayConfig(name, a, start))
		}()

		go func() {
			defer wg.Done()

			days := 2
			_, _ = m.UpdateArchive(ctx, &driver.ArchiveUpdate{Name: "arch", RetentionDays: &days})
			_, _ = m.ListArchives(ctx, driver.ArchiveFilter{})
		}()
	}

	wg.Wait()

	replays, err := m.ListReplays(ctx, driver.ReplayFilter{State: "COMPLETED"})
	require.NoError(t, err)
	assert.Len(t, replays, 4)
}
