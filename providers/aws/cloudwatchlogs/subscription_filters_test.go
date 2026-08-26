package cloudwatchlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingInvoker captures subscription-filter -> Lambda deliveries.
type recordingInvoker struct {
	mu       sync.Mutex
	arns     []string
	payloads [][]byte
}

func (i *recordingInvoker) InvokeExternal(_ context.Context, functionARN string, payload []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.arns = append(i.arns, functionARN)
	i.payloads = append(i.payloads, payload)

	return nil
}

// decodeAWSLogs unwraps the awslogs envelope (base64 + gzip) into the decoded
// CloudWatch Logs subscription event.
func decodeAWSLogs(t *testing.T, payload []byte) cwlSubscriptionEvent {
	t.Helper()

	var env awslogsEnvelope
	require.NoError(t, json.Unmarshal(payload, &env))

	gzipped, err := base64.StdEncoding.DecodeString(env.AWSLogs.Data)
	require.NoError(t, err)

	gr, err := gzip.NewReader(bytes.NewReader(gzipped))
	require.NoError(t, err)

	raw, err := io.ReadAll(gr)
	require.NoError(t, err)

	var ev cwlSubscriptionEvent
	require.NoError(t, json.Unmarshal(raw, &ev))

	return ev
}

const lambdaDestARN = "arn:aws:lambda:us-east-1:000000000000:function:log-sub"

func TestSubscriptionFilterCRUD(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name:           "errors",
		LogGroup:       "test-group",
		FilterPattern:  "ERROR",
		DestinationARN: lambdaDestARN,
		RoleARN:        "arn:aws:iam::000000000000:role/cwl",
		Distribution:   "ByLogStream",
	}))

	got, err := m.DescribeSubscriptionFilters(ctx, "test-group")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "errors", got[0].Name)
	assert.Equal(t, "ERROR", got[0].FilterPattern)
	assert.Equal(t, lambdaDestARN, got[0].DestinationARN)
	assert.Equal(t, "arn:aws:iam::000000000000:role/cwl", got[0].RoleARN)
	assert.Equal(t, "ByLogStream", got[0].Distribution)
	assert.False(t, got[0].CreatedAt.IsZero())

	// Update in place (same name) keeps a single filter.
	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "errors", LogGroup: "test-group", FilterPattern: "FATAL", DestinationARN: lambdaDestARN,
	}))

	got, err = m.DescribeSubscriptionFilters(ctx, "test-group")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "FATAL", got[0].FilterPattern)

	require.NoError(t, m.DeleteSubscriptionFilter(ctx, "test-group", "errors"))

	got, err = m.DescribeSubscriptionFilters(ctx, "test-group")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSubscriptionFilterValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	// Missing group.
	err := m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "f", LogGroup: "nope", DestinationARN: lambdaDestARN,
	})
	assert.True(t, errors.IsNotFound(err))

	// Missing name.
	err = m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		LogGroup: "test-group", DestinationARN: lambdaDestARN,
	})
	assert.True(t, errors.IsInvalidArgument(err))

	// Missing destinationArn.
	err = m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "f", LogGroup: "test-group",
	})
	assert.True(t, errors.IsInvalidArgument(err))

	// Delete missing filter.
	err = m.DeleteSubscriptionFilter(ctx, "test-group", "ghost")
	assert.True(t, errors.IsNotFound(err))
}

// TestSubscriptionFilterLimit pins the two-filters-per-group quota.
func TestSubscriptionFilterLimit(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	setupGroupAndStream(t, m)

	for _, name := range []string{"f1", "f2"} {
		require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
			Name: name, LogGroup: "test-group", DestinationARN: lambdaDestARN,
		}))
	}

	err := m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "f3", LogGroup: "test-group", DestinationARN: lambdaDestARN,
	})
	require.Error(t, err)
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(err))

	// Updating an existing filter still succeeds at the limit.
	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "f1", LogGroup: "test-group", FilterPattern: "x", DestinationARN: lambdaDestARN,
	}))
}

// TestSubscriptionFilterLambdaDeliveryOnMatch is the core cross-service check:
// a PutLogEvents whose message matches the subscription filter delivers the
// matched events (as an awslogs payload) to the mapped Lambda; a non-matching
// message does not.
func TestSubscriptionFilterLambdaDeliveryOnMatch(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	inv := &recordingInvoker{}
	m.SetLambdaInvoker(inv)
	setupGroupAndStream(t, m)

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "errors", LogGroup: "test-group", FilterPattern: "ERROR", DestinationARN: lambdaDestARN,
	}))

	// Matching + non-matching events in one batch; only the matching one is delivered.
	require.NoError(t, m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
		{Timestamp: m.opts.Clock.Now(), Message: "ERROR boom"},
		{Timestamp: m.opts.Clock.Now(), Message: "info: ok"},
	}))

	inv.mu.Lock()
	require.Len(t, inv.arns, 1, "expected exactly one delivery")
	assert.Equal(t, lambdaDestARN, inv.arns[0])
	payload := inv.payloads[0]
	inv.mu.Unlock()

	ev := decodeAWSLogs(t, payload)
	assert.Equal(t, "DATA_MESSAGE", ev.MessageType)
	assert.Equal(t, "test-group", ev.LogGroup)
	assert.Equal(t, "test-stream", ev.LogStream)
	assert.Equal(t, []string{"errors"}, ev.SubscriptionFilters)
	require.Len(t, ev.LogEvents, 1)
	assert.Equal(t, "ERROR boom", ev.LogEvents[0].Message)

	// A batch with no matching message delivers nothing further.
	require.NoError(t, m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
		{Timestamp: m.opts.Clock.Now(), Message: "all good"},
	}))

	inv.mu.Lock()
	assert.Len(t, inv.arns, 1, "non-matching batch must not deliver")
	inv.mu.Unlock()
}

// TestSubscriptionFilterDeleteStopsDelivery confirms a deleted filter no longer
// delivers matching events.
func TestSubscriptionFilterDeleteStopsDelivery(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	inv := &recordingInvoker{}
	m.SetLambdaInvoker(inv)
	setupGroupAndStream(t, m)

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name: "errors", LogGroup: "test-group", FilterPattern: "ERROR", DestinationARN: lambdaDestARN,
	}))
	require.NoError(t, m.DeleteSubscriptionFilter(ctx, "test-group", "errors"))

	require.NoError(t, m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
		{Timestamp: m.opts.Clock.Now(), Message: "ERROR boom"},
	}))

	inv.mu.Lock()
	assert.Empty(t, inv.arns, "deleted filter must not deliver")
	inv.mu.Unlock()
}

// TestSubscriptionFilterKinesisNotDelivered documents that a non-Lambda
// destination (Kinesis) is not yet wired: matching events are not invoked
// through the Lambda path (delivery deferred).
func TestSubscriptionFilterKinesisNotDelivered(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	inv := &recordingInvoker{}
	m.SetLambdaInvoker(inv)
	setupGroupAndStream(t, m)

	require.NoError(t, m.PutSubscriptionFilter(ctx, &driver.SubscriptionFilterConfig{
		Name:           "to-kinesis",
		LogGroup:       "test-group",
		FilterPattern:  "ERROR",
		DestinationARN: "arn:aws:kinesis:us-east-1:000000000000:stream/logs",
	}))

	require.NoError(t, m.PutLogEvents(ctx, "test-group", "test-stream", []driver.LogEvent{
		{Timestamp: m.opts.Clock.Now(), Message: "ERROR boom"},
	}))

	inv.mu.Lock()
	assert.Empty(t, inv.arns, "Kinesis destination is deferred; Lambda invoker must not fire")
	inv.mu.Unlock()
}
