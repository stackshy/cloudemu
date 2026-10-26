package kinesis_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"

	"github.com/stackshy/cloudemu/v2"
	kinesisserver "github.com/stackshy/cloudemu/v2/server/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// liveFixture is a Kinesis wire server whose SubscribeToShard lifetime and poll
// interval are shortened, plus a count of SubscribeToShard handlers still
// running so tests can prove a subscription's handler returns (no leak).
type liveFixture struct {
	c      *awskinesis.Client
	active atomic.Int32
}

func newLiveFixture(t *testing.T, lifetime time.Duration) *liveFixture {
	t.Helper()

	return newLiveFixtureWith(t, lifetime, nil, nil)
}

// newLiveFixtureWith is newLiveFixture plus an optional handler tweak (e.g. a
// shorter heartbeat) and an optional shutdown notifier stamped onto every
// request's base context, the way cloudemu serve does.
func newLiveFixtureWith(
	t *testing.T, lifetime time.Duration, tweak func(*kinesisserver.Handler), shutdown *wire.ShutdownNotifier,
) *liveFixture {
	t.Helper()

	cloud := cloudemu.NewAWS()
	h := kinesisserver.New(cloud.Kinesis)
	h.SetSubscriptionTimings(lifetime, 20*time.Millisecond)

	if tweak != nil {
		tweak(h)
	}

	f := &liveFixture{}

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Header.Get("X-Amz-Target"), ".SubscribeToShard") {
			f.active.Add(1)
			defer f.active.Add(-1)
		}

		h.ServeHTTP(w, r)
	}))

	if shutdown != nil {
		ts.Config.BaseContext = func(net.Listener) context.Context { return shutdown.Context(context.Background()) }
	}

	ts.Start()
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	f.c = awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.HTTPClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	})

	return f
}

// waitNoActive fails unless every SubscribeToShard handler has returned.
func (f *liveFixture) waitNoActive(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for f.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d SubscribeToShard handler(s) still running", f.active.Load())
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// subscribe creates a 1-shard stream with a registered consumer and opens a
// subscription at the given starting position.
func (f *liveFixture) subscribe(
	t *testing.T, stream string, pos kinesistypes.ShardIteratorType,
) (*awskinesis.SubscribeToShardEventStream, string) {
	t.Helper()

	ctx := context.Background()
	mustCreate(t, f.c, stream, 1)

	sum, err := f.c.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{StreamName: aws.String(stream)})
	if err != nil {
		t.Fatalf("DescribeStreamSummary: %v", err)
	}

	reg, err := f.c.RegisterStreamConsumer(ctx, &awskinesis.RegisterStreamConsumerInput{
		StreamARN: sum.StreamDescriptionSummary.StreamARN, ConsumerName: aws.String("efo"),
	})
	if err != nil {
		t.Fatalf("RegisterStreamConsumer: %v", err)
	}

	ls, err := f.c.ListShards(ctx, &awskinesis.ListShardsInput{StreamName: aws.String(stream)})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	shardID := aws.ToString(ls.Shards[0].ShardId)

	out, err := f.c.SubscribeToShard(ctx, &awskinesis.SubscribeToShardInput{
		ConsumerARN:      reg.Consumer.ConsumerARN,
		ShardId:          aws.String(shardID),
		StartingPosition: &kinesistypes.StartingPosition{Type: pos},
	})
	if err != nil {
		t.Fatalf("SubscribeToShard: %v", err)
	}

	return out.GetStream(), shardID
}

// nextEvent returns the next SubscribeToShardEvent, or nil when the stream ends.
func nextEvent(t *testing.T, s *awskinesis.SubscribeToShardEventStream) *kinesistypes.SubscribeToShardEvent {
	t.Helper()

	select {
	case ev, ok := <-s.Events():
		if !ok {
			return nil
		}

		e, isEvent := ev.(*kinesistypes.SubscribeToShardEventStreamMemberSubscribeToShardEvent)
		if !isEvent {
			t.Fatalf("unexpected event %T", ev)
		}

		return &e.Value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a SubscribeToShardEvent")

		return nil
	}
}

func putData(t *testing.T, c *awskinesis.Client, stream, data string) {
	t.Helper()

	if _, err := c.PutRecord(context.Background(), &awskinesis.PutRecordInput{
		StreamName: aws.String(stream), PartitionKey: aws.String("pk"), Data: []byte(data),
	}); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}
}

// TestSDKSubscribeToShardPushesLiveRecords: real Kinesis keeps the HTTP/2
// subscription open and pushes records as they are written, not one event and
// close. Records written after subscribing (from LATEST) must arrive on the
// same stream, in order, and closing the stream must end the server handler.
func TestSDKSubscribeToShardPushesLiveRecords(t *testing.T) {
	f := newLiveFixture(t, time.Minute)

	stream, _ := f.subscribe(t, "live", kinesistypes.ShardIteratorTypeLatest)

	first := nextEvent(t, stream)
	if first == nil || len(first.Records) != 0 || aws.ToString(first.ContinuationSequenceNumber) == "" {
		t.Fatalf("initial LATEST event = %+v, want empty records with a continuation", first)
	}

	for _, want := range []string{"one", "two"} {
		putData(t, f.c, "live", want)

		ev := nextEvent(t, stream)
		if ev == nil {
			t.Fatalf("stream closed before %q was pushed", want)
		}

		if len(ev.Records) != 1 || string(ev.Records[0].Data) != want {
			t.Fatalf("pushed records = %+v, want exactly %q", ev.Records, want)
		}

		if aws.ToString(ev.ContinuationSequenceNumber) != aws.ToString(ev.Records[0].SequenceNumber) {
			t.Fatalf("continuation %q, want last record seq %q",
				aws.ToString(ev.ContinuationSequenceNumber), aws.ToString(ev.Records[0].SequenceNumber))
		}
	}

	if f.active.Load() != 1 {
		t.Fatalf("active subscriptions = %d, want 1 while the client reads", f.active.Load())
	}

	// Client disconnect ends the server-side subscription.
	if err := stream.Close(); err != nil && !benignStreamTeardown(err) {
		t.Fatalf("Close: %v", err)
	}

	f.waitNoActive(t)
}

// TestSDKSubscribeToShardEndsAfterLifetime: a subscription lasts a bounded time
// (5 minutes in real Kinesis, shortened here) and then the server closes it.
func TestSDKSubscribeToShardEndsAfterLifetime(t *testing.T) {
	f := newLiveFixture(t, 300*time.Millisecond)

	stream, _ := f.subscribe(t, "expire", kinesistypes.ShardIteratorTypeTrimHorizon)
	defer stream.Close()

	if nextEvent(t, stream) == nil {
		t.Fatal("want the initial event before expiry")
	}

	if ev := nextEvent(t, stream); ev != nil {
		t.Fatalf("want the stream to close at the end of its lifetime, got event %+v", ev)
	}

	if err := stream.Err(); err != nil && !benignStreamTeardown(err) {
		t.Fatalf("stream error: %v", err)
	}

	f.waitNoActive(t)
}

// TestSDKSubscribeToShardSignalsShardEnd: when the subscribed shard is closed by
// a reshard, the final event carries ChildShards and no continuation sequence
// number (the shard-end contract KCL validates), then the stream ends.
func TestSDKSubscribeToShardSignalsShardEnd(t *testing.T) {
	f := newLiveFixture(t, time.Minute)

	stream, _ := f.subscribe(t, "reshard", kinesistypes.ShardIteratorTypeTrimHorizon)
	defer stream.Close()

	if nextEvent(t, stream) == nil {
		t.Fatal("want the initial event")
	}

	putData(t, f.c, "reshard", "before-close")

	if _, err := f.c.UpdateShardCount(context.Background(), &awskinesis.UpdateShardCountInput{
		StreamName: aws.String("reshard"), TargetShardCount: aws.Int32(2),
		ScalingType: kinesistypes.ScalingTypeUniformScaling,
	}); err != nil {
		t.Fatalf("UpdateShardCount: %v", err)
	}

	var end *kinesistypes.SubscribeToShardEvent

	for {
		ev := nextEvent(t, stream)
		if ev == nil {
			break
		}

		end = ev
	}

	if end == nil || len(end.ChildShards) != 2 {
		t.Fatalf("final event = %+v, want 2 child shards", end)
	}

	if end.ContinuationSequenceNumber != nil {
		t.Fatalf("shard-end continuation = %q, want nil", aws.ToString(end.ContinuationSequenceNumber))
	}

	f.waitNoActive(t)
}

// TestSDKSubscribeToShardHeartbeatsWhenIdle: real enhanced fan-out keeps
// sending empty SubscribeToShardEvents on a quiet shard (KCL restarts a
// subscription that delivers nothing for ~35s), each carrying the unchanged
// continuation so the consumer's checkpoint still advances.
func TestSDKSubscribeToShardHeartbeatsWhenIdle(t *testing.T) {
	f := newLiveFixtureWith(t, time.Minute, func(h *kinesisserver.Handler) {
		h.SetSubscriptionHeartbeat(100 * time.Millisecond)
	}, nil)

	stream, _ := f.subscribe(t, "idle", kinesistypes.ShardIteratorTypeLatest)
	defer stream.Close()

	first := nextEvent(t, stream)
	if first == nil {
		t.Fatal("want the initial event")
	}

	cont := aws.ToString(first.ContinuationSequenceNumber)

	for i := range 2 {
		ev := nextEvent(t, stream)
		if ev == nil {
			t.Fatalf("heartbeat %d: stream closed on an idle shard", i)
		}

		if len(ev.Records) != 0 || aws.ToString(ev.ContinuationSequenceNumber) != cont {
			t.Fatalf("heartbeat %d = %+v, want empty records with continuation %q", i, ev, cont)
		}
	}

	// A record written after heartbeats still arrives with its own continuation.
	putData(t, f.c, "idle", "late")

	ev := nextEvent(t, stream)
	for ev != nil && len(ev.Records) == 0 {
		ev = nextEvent(t, stream)
	}

	if ev == nil || string(ev.Records[0].Data) != "late" {
		t.Fatalf("event after heartbeats = %+v, want the %q record", ev, "late")
	}
}

// TestSDKSubscribeToShardEndsOnServerShutdown: when the serving process begins
// a graceful shutdown, open subscriptions end promptly instead of holding the
// shutdown open until its timeout (the subscription lifetime here is a minute).
func TestSDKSubscribeToShardEndsOnServerShutdown(t *testing.T) {
	shutdown := wire.NewShutdownNotifier()
	f := newLiveFixtureWith(t, time.Minute, nil, shutdown)

	stream, _ := f.subscribe(t, "shutdown", kinesistypes.ShardIteratorTypeLatest)
	defer stream.Close()

	if nextEvent(t, stream) == nil {
		t.Fatal("want the initial event")
	}

	shutdown.Notify()

	if ev := nextEvent(t, stream); ev != nil {
		t.Fatalf("want the stream to end on shutdown, got event %+v", ev)
	}

	f.waitNoActive(t)
}
