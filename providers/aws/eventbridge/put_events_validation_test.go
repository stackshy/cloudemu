package eventbridge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutEventEntriesRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		event   driver.Event
		code    string
		message string
	}{
		{
			name:    "missing source",
			event:   driver.Event{DetailType: "t", Detail: "{}"},
			code:    "InvalidArgument",
			message: "Parameter Source is not valid. Reason: Source is a required argument.",
		},
		{
			name:    "missing detail type",
			event:   driver.Event{Source: "s", Detail: "{}"},
			code:    "InvalidArgument",
			message: "Parameter DetailType is not valid. Reason: DetailType is a required argument.",
		},
		{
			name:    "missing detail",
			event:   driver.Event{Source: "s", DetailType: "t"},
			code:    "InvalidArgument",
			message: "Parameter Detail is not valid. Reason: Detail is a required argument.",
		},
		{
			name:    "source checked before detail type",
			event:   driver.Event{},
			code:    "InvalidArgument",
			message: "Parameter Source is not valid. Reason: Source is a required argument.",
		},
		{name: "detail is an array", event: driver.Event{Source: "s", DetailType: "t", Detail: "[]"}, code: "MalformedDetail", message: "Detail is malformed."},
		{name: "detail is null", event: driver.Event{Source: "s", DetailType: "t", Detail: "null"}, code: "MalformedDetail", message: "Detail is malformed."},
		{name: "detail is a string", event: driver.Event{Source: "s", DetailType: "t", Detail: `"x"`}, code: "MalformedDetail", message: "Detail is malformed."},
		{name: "detail is not json", event: driver.Event{Source: "s", DetailType: "t", Detail: "{"}, code: "MalformedDetail", message: "Detail is malformed."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()

			results, err := m.PutEventEntries(context.Background(), []driver.Event{tc.event})
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Empty(t, results[0].EventID)
			assert.Equal(t, tc.code, results[0].ErrorCode)
			assert.Equal(t, tc.message, results[0].ErrorMessage)

			history, err := m.GetEventHistory(context.Background(), "", 0)
			require.NoError(t, err)
			assert.Empty(t, history)
		})
	}
}

func TestPutEventEntriesAlignedByIndex(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	results, err := m.PutEventEntries(ctx, []driver.Event{
		{Source: "s", DetailType: "t", Detail: "{}", EventBus: "ghost"},
		{DetailType: "t", Detail: "{}"},
		{Source: "s", DetailType: "kept", Detail: `{"k":1}`},
		{Source: "s", DetailType: "t", Detail: "nope"},
	})
	require.NoError(t, err)
	require.Len(t, results, 4)

	assert.NotEmpty(t, results[0].EventID)
	assert.Empty(t, results[0].ErrorCode)
	assert.Equal(t, "InvalidArgument", results[1].ErrorCode)
	assert.NotEmpty(t, results[2].EventID)
	assert.Equal(t, "MalformedDetail", results[3].ErrorCode)

	history, err := m.GetEventHistory(ctx, "", 0)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, results[2].EventID, history[0].ID)
}

func TestPutEventsNonexistentBusNotDelivered(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	result, err := m.PutEvents(ctx, []driver.Event{
		{Source: "s", DetailType: "t", Detail: "{}", EventBus: "ghost"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.SuccessCount)
	assert.Equal(t, 0, result.FailCount)
	require.Len(t, result.EventIDs, 1)

	_, err = m.GetEventHistory(ctx, "ghost", 0)
	assert.True(t, cerrors.IsNotFound(err), "the dropped event must not create the bus")
}

func TestPutEventEntriesEntryCount(t *testing.T) {
	m, _ := newTestMock()

	_, err := m.PutEventEntries(context.Background(), nil)
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))

	events := make([]driver.Event, 11)
	for i := range events {
		events[i] = driver.Event{Source: "s", DetailType: "t", Detail: "{}"}
	}

	_, err = m.PutEventEntries(context.Background(), events)
	require.Error(t, err)
	assert.True(t, cerrors.IsInvalidArgument(err))
}

// detailOfSize returns a JSON object Detail whose encoded length is n bytes.
func detailOfSize(n int) string {
	const wrapper = len(`{"p":""}`)

	return `{"p":"` + strings.Repeat("x", n-wrapper) + `"}`
}

func TestPutEventEntriesSizeLimit(t *testing.T) {
	const limit = 1 << 20

	tests := []struct {
		name    string
		region  string
		size    int // total request size in bytes
		withErr bool
	}{
		{name: "just under 1 MB", region: "us-east-1", size: limit - 1},
		{name: "exactly 1 MB", region: "us-east-1", size: limit, withErr: true},
		{name: "over 256 KB is fine in a 1 MB region", region: "eu-west-1", size: 300 << 10},
		{name: "256 KB region rejects at 256 KB", region: "ap-southeast-5", size: 256 << 10, withErr: true},
		{name: "256 KB region accepts under 256 KB", region: "mx-central-1", size: (256 << 10) - 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := config.NewOptions(config.WithRegion(tc.region))
			m := New(opts)

			// Source "s" and DetailType "t" take 2 bytes of the total.
			event := driver.Event{Source: "s", DetailType: "t", Detail: detailOfSize(tc.size - 2)}

			results, err := m.PutEventEntries(context.Background(), []driver.Event{event})
			if !tc.withErr {
				require.NoError(t, err)
				require.Len(t, results, 1)
				assert.NotEmpty(t, results[0].EventID)

				return
			}

			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err))
			assert.Equal(t, "Total size of the entries in the request is over the limit.", cerrors.Message(err))
		})
	}
}

func TestPutEventEntriesSizeCountsAllEntriesAndTime(t *testing.T) {
	m, _ := newTestMock()

	// Two entries that each fit, but not together.
	half := driver.Event{Source: "s", DetailType: "t", Detail: detailOfSize(1 << 19)}

	_, err := m.PutEventEntries(context.Background(), []driver.Event{half, half})
	require.Error(t, err)

	// A set Time adds 14 bytes, which tips an entry sitting 10 bytes under the limit.
	event := driver.Event{Source: "s", DetailType: "t", Detail: detailOfSize((1 << 20) - 12)}

	_, err = m.PutEventEntries(context.Background(), []driver.Event{event})
	require.NoError(t, err)

	event.Time = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	_, err = m.PutEventEntries(context.Background(), []driver.Event{event})
	require.Error(t, err)
}

func TestPutTargetsInputModesMutuallyExclusive(t *testing.T) {
	const transformer = `{"InputTemplate":"\"x\""}`

	tests := []struct {
		name   string
		target driver.Target
	}{
		{name: "input and input path", target: driver.Target{ID: "t1", ARN: "arn", Input: "{}", InputPath: "$.detail"}},
		{name: "input and transformer", target: driver.Target{ID: "t1", ARN: "arn", Input: "{}", InputTransformer: transformer}},
		{name: "input path and transformer", target: driver.Target{ID: "t1", ARN: "arn", InputPath: "$.detail", InputTransformer: transformer}},
		{
			name:   "all three",
			target: driver.Target{ID: "t1", ARN: "arn", Input: "{}", InputPath: "$.detail", InputTransformer: transformer},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMock()
			ctx := context.Background()
			createTestRule(t, m, "", "r1")

			err := m.PutTargets(ctx, "", "r1", []driver.Target{{ID: "ok", ARN: "arn", Input: "{}"}, tc.target})
			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err))
			assert.Equal(t, "Only one of Input, InputPath, or InputTransformer must be provided for target t1.", cerrors.Message(err))

			targets, err := m.ListTargets(ctx, "", "r1")
			require.NoError(t, err)
			assert.Empty(t, targets, "a rejected request must not store any target")
		})
	}

	t.Run("one mode per target is accepted", func(t *testing.T) {
		m, _ := newTestMock()
		createTestRule(t, m, "", "r1")

		err := m.PutTargets(context.Background(), "", "r1", []driver.Target{
			{ID: "a", ARN: "arn", Input: "{}"},
			{ID: "b", ARN: "arn", InputPath: "$.detail"},
			{ID: "c", ARN: "arn", InputTransformer: transformer},
			{ID: "d", ARN: "arn"},
		})
		require.NoError(t, err)
	})
}
