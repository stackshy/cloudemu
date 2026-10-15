package kinesis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

const (
	contentTypeEventStream = "application/vnd.amazon.eventstream"
	contentTypeJSON        = "application/json"
	eventTypeSubscribe     = "SubscribeToShardEvent"
	// eventTypeInitial is the first frame Kinesis sends on a SubscribeToShard
	// connection. The SDK blocks its SubscribeToShard call until it arrives, so it
	// must precede any data event.
	eventTypeInitial = "initial-response"

	// defaultSubscriptionLifetime is how long one SubscribeToShard connection
	// stays open: real Kinesis pushes events "for up to 5 minutes, after which
	// time you need to call SubscribeToShard again to renew the subscription".
	defaultSubscriptionLifetime = 5 * time.Minute
	// defaultSubscriptionPoll is how often an open subscription checks the shard
	// for newly written records to push.
	defaultSubscriptionPoll = 200 * time.Millisecond
)

// startingPositionJSON is the {Type,SequenceNumber,Timestamp} wire shape of a
// SubscribeToShard StartingPosition. Timestamp is epoch seconds.
type startingPositionJSON struct {
	Type           string   `json:"Type"`
	SequenceNumber string   `json:"SequenceNumber"`
	Timestamp      *float64 `json:"Timestamp"`
}

type subscribeToShardRequest struct {
	ConsumerARN      string               `json:"ConsumerARN"`
	ShardID          string               `json:"ShardId"`
	StartingPosition startingPositionJSON `json:"StartingPosition"`
}

// subscribeToShardEventJSON is the JSON payload of one SubscribeToShardEvent
// frame. Data on each record is a base64 blob (encoding/json renders []byte as
// base64), matching the SDK's blob decoding.
type subscribeToShardEventJSON struct {
	Records                    []recordJSON     `json:"Records"`
	ContinuationSequenceNumber *string          `json:"ContinuationSequenceNumber,omitempty"`
	MillisBehindLatest         int64            `json:"MillisBehindLatest"`
	ChildShards                []childShardJSON `json:"ChildShards,omitempty"`
}

// subscribeToShard handles Kinesis_20131202.SubscribeToShard. It resolves the
// subscription against the driver, then holds an
// application/vnd.amazon.eventstream response open, pushing a
// SubscribeToShardEvent for the records available now and another whenever new
// records land, until the shard ends, the subscription lifetime (5 minutes)
// elapses, or the client disconnects.
func (h *Handler) subscribeToShard(w http.ResponseWriter, r *http.Request) {
	var req subscribeToShardRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidArgumentException", err.Error())

		return
	}

	var ts time.Time
	if req.StartingPosition.Timestamp != nil {
		ts = time.Unix(int64(*req.StartingPosition.Timestamp), 0).UTC()
	}

	res, err := h.kinesis.SubscribeToShard(r.Context(), kinesisdriver.SubscribeToShardInput{
		ConsumerARN:            req.ConsumerARN,
		ShardID:                req.ShardID,
		StartingPositionType:   req.StartingPosition.Type,
		StartingSequenceNumber: req.StartingPosition.SequenceNumber,
		StartingTimestamp:      ts,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	es := startEventStream(w)
	if es.writeSubscribeEvent(res) != nil || res.ContinuationSequenceNumber == "" {
		return
	}

	h.pushSubscription(r.Context(), es, req.ConsumerARN, req.ShardID, res.ContinuationSequenceNumber)
}

// pushSubscription keeps a subscription live: every poll interval it reads the
// records written after the continuation cursor and pushes them as one event.
// It runs on the request goroutine (no goroutines to leak) and returns when the
// client goes away (ctx), the lifetime elapses, the shard ends, the write
// fails, or the shard/consumer disappears (pushed as an in-stream exception).
func (h *Handler) pushSubscription(ctx context.Context, es *eventStream, consumerARN, shardID, cont string) {
	lifetime := time.NewTimer(h.subscriptionLifetime)
	defer lifetime.Stop()

	tick := time.NewTicker(h.subscriptionPoll)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-lifetime.C:
			return
		case <-tick.C:
		}

		res, err := h.kinesis.SubscribeToShard(ctx, kinesisdriver.SubscribeToShardInput{
			ConsumerARN:            consumerARN,
			ShardID:                shardID,
			StartingPositionType:   kinesisdriver.IteratorAfterSequenceNumber,
			StartingSequenceNumber: cont,
		})
		if err != nil {
			es.writeException(err)

			return
		}

		// Nothing new on an open shard: keep waiting rather than pushing an
		// empty event every tick.
		if len(res.Records) == 0 && res.ContinuationSequenceNumber != "" {
			continue
		}

		if es.writeSubscribeEvent(res) != nil || res.ContinuationSequenceNumber == "" {
			return
		}

		cont = res.ContinuationSequenceNumber
	}
}

// eventStream is an open application/vnd.amazon.eventstream response.
type eventStream struct {
	w       http.ResponseWriter
	enc     *eventstream.Encoder
	flusher http.Flusher
}

// startEventStream writes the eventstream response headers and the
// initial-response frame the SDK's SubscribeToShard call blocks on.
func startEventStream(w http.ResponseWriter) *eventStream {
	w.Header().Set("Content-Type", contentTypeEventStream)
	// Deliberately do NOT force "Connection: close" here. Closing the connection
	// as the handler returns races the client's in-flight read of the final
	// frame, intermittently dropping it under CI load; normal keep-alive chunked
	// termination delivers every frame first. Any later teardown of the pooled
	// connection surfaces as a benign "use of closed network connection" that the
	// stream tests tolerate, never as lost data.
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	es := &eventStream{w: w, enc: eventstream.NewEncoder(), flusher: flusher}

	// The SDK's SubscribeToShard call blocks until it reads the initial-response
	// event, so emit it (empty SubscribeToShardOutput body) before any data.
	_ = es.writeEvent(eventTypeInitial, []byte("{}"))

	return es
}

// writeSubscribeEvent frames res as one SubscribeToShardEvent. An empty
// continuation (shard end) is omitted on the wire, i.e. null, with ChildShards.
func (es *eventStream) writeSubscribeEvent(res *kinesisdriver.SubscribeToShardResult) error {
	ev := subscribeToShardEventJSON{
		Records:            recordsToWire(res.Records),
		MillisBehindLatest: res.MillisBehindLatest,
		ChildShards:        childShardsToWire(res.ChildShards),
	}

	if res.ContinuationSequenceNumber != "" {
		ev.ContinuationSequenceNumber = &res.ContinuationSequenceNumber
	}

	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	return es.writeEvent(eventTypeSubscribe, payload)
}

// writeEvent frames one JSON event of the given type and flushes it.
func (es *eventStream) writeEvent(eventType string, payload []byte) error {
	var hdr eventstream.Headers

	hdr.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
	hdr.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue(eventType))
	hdr.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	return es.encode(eventstream.Message{Headers: hdr, Payload: payload})
}

// writeException pushes err as an in-stream exception event (the
// SubscribeToShardEventStream carries ResourceNotFoundException,
// ResourceInUseException, ... members), which the SDK surfaces from Err().
func (es *eventStream) writeException(err error) {
	exType := "InternalFailureException"

	var apiErr *kinesisdriver.APIError

	switch {
	case errors.As(err, &apiErr):
		exType = apiErr.Exception
	case cerrors.IsNotFound(err):
		exType = "ResourceNotFoundException"
	case cerrors.IsAlreadyExists(err), cerrors.IsFailedPrecondition(err):
		exType = "ResourceInUseException"
	}

	payload, mErr := json.Marshal(map[string]string{"message": cerrors.Message(err)})
	if mErr != nil {
		return
	}

	var hdr eventstream.Headers

	hdr.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.ExceptionMessageType))
	hdr.Set(eventstreamapi.ExceptionTypeHeader, eventstream.StringValue(exType))
	hdr.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	_ = es.encode(eventstream.Message{Headers: hdr, Payload: payload})
}

func (es *eventStream) encode(msg eventstream.Message) error {
	if err := es.enc.Encode(es.w, msg); err != nil {
		return err
	}

	if es.flusher != nil {
		es.flusher.Flush()
	}

	return nil
}
