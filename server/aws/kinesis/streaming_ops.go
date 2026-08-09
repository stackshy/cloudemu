package kinesis

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"

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
	Records                    []recordJSON `json:"Records"`
	ContinuationSequenceNumber string       `json:"ContinuationSequenceNumber"`
	MillisBehindLatest         int64        `json:"MillisBehindLatest"`
}

// subscribeToShard handles Kinesis_20131202.SubscribeToShard. It resolves the
// subscription against the driver, then writes an HTTP/2-style
// application/vnd.amazon.eventstream response carrying a single
// SubscribeToShardEvent frame before returning.
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

	writeSubscribeEvent(w, res)
}

// writeSubscribeEvent frames res as a single SubscribeToShardEvent over the
// eventstream protocol.
func writeSubscribeEvent(w http.ResponseWriter, res *kinesisdriver.SubscribeToShardResult) {
	w.Header().Set("Content-Type", contentTypeEventStream)
	// Deliberately do NOT force "Connection: close" here. Closing the connection
	// as the handler returns races the client's in-flight read of the final
	// frame, intermittently dropping it under CI load; normal keep-alive chunked
	// termination delivers every frame first. Any later teardown of the pooled
	// connection surfaces as a benign "use of closed network connection" that the
	// stream tests tolerate, never as lost data.
	w.WriteHeader(http.StatusOK)

	enc := eventstream.NewEncoder()
	flusher, _ := w.(http.Flusher)

	// The SDK's SubscribeToShard call blocks until it reads the initial-response
	// event, so emit it (empty SubscribeToShardOutput body) before any data.
	writeEvent(w, enc, flusher, eventTypeInitial, []byte("{}"))

	payload, err := json.Marshal(subscribeToShardEventJSON{
		Records:                    recordsToWire(res.Records),
		ContinuationSequenceNumber: res.ContinuationSequenceNumber,
		MillisBehindLatest:         res.MillisBehindLatest,
	})
	if err != nil {
		return
	}

	writeEvent(w, enc, flusher, eventTypeSubscribe, payload)
}

// writeEvent frames one JSON event of the given type and flushes it.
func writeEvent(w http.ResponseWriter, enc *eventstream.Encoder, flusher http.Flusher, eventType string, payload []byte) {
	var hdr eventstream.Headers

	hdr.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
	hdr.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue(eventType))
	hdr.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	_ = enc.Encode(w, eventstream.Message{Headers: hdr, Payload: payload})

	if flusher != nil {
		flusher.Flush()
	}
}
