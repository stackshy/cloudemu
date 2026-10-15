package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
)

// Output headers of the streaming operations (see the SDK deserializers).
const (
	headerAgentSessionID   = "x-amz-bedrock-agent-session-id"
	headerAgentContentType = "x-amzn-bedrock-agent-content-type"
	headerAgentMemoryID    = "x-amz-bedrock-agent-memory-id"
	headerFlowExecutionID  = "x-amz-bedrock-flow-execution-id"
	headerKBSessionID      = "x-amzn-bedrock-knowledge-base-session-id"
)

// streamEvent is one eventstream event: its :event-type and JSON payload.
type streamEvent struct {
	eventType string
	payload   any
}

// writeEventStream writes a 200 eventstream response with the given output
// headers and events, in order.
func writeEventStream(w http.ResponseWriter, headers map[string]string, events []streamEvent) {
	w.Header().Set("Content-Type", contentTypeEventStream)

	for k, v := range headers {
		if v != "" {
			w.Header().Set(k, v)
		}
	}

	w.WriteHeader(http.StatusOK)

	enc := eventstream.NewEncoder()

	for _, e := range events {
		body, err := json.Marshal(e.payload)
		if err != nil {
			return
		}

		var h eventstream.Headers

		h.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
		h.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue(e.eventType))
		h.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

		_ = enc.Encode(w, eventstream.Message{Headers: h, Payload: body})
	}

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
