package bedrockagentruntime

import (
	"encoding/base64"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"

	bedrockagentruntimedriver "github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// InvokeAgent output header names (see the SDK deserializer header bindings).
const (
	headerAgentSessionID   = "x-amz-bedrock-agent-session-id"
	headerAgentContentType = "x-amzn-bedrock-agent-content-type"
)

// writeInvokeAgentStream writes the InvokeAgent response as an
// application/vnd.amazon.eventstream body: a single chunk event whose JSON
// payload {"bytes":"<base64>"} decodes to the completion text. The SDK
// reconstructs the completion by concatenating each chunk's decoded bytes.
func writeInvokeAgentStream(w http.ResponseWriter, res *bedrockagentruntimedriver.InvokeAgentResult) {
	ct := res.ContentType
	if ct == "" {
		ct = contentTypeJSON
	}

	w.Header().Set("Content-Type", contentTypeEventStream)
	w.Header().Set(headerAgentSessionID, res.SessionID)
	w.Header().Set(headerAgentContentType, ct)
	w.WriteHeader(http.StatusOK)

	enc := eventstream.NewEncoder()
	flusher, _ := w.(http.Flusher)

	writeChunk(w, enc, []byte(res.Completion))

	if flusher != nil {
		flusher.Flush()
	}
}

// writeChunk encodes a single "chunk" event carrying textBytes.
func writeChunk(w http.ResponseWriter, enc *eventstream.Encoder, textBytes []byte) {
	var h eventstream.Headers

	h.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
	h.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue("chunk"))
	h.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	payload := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString(textBytes) + `"}`)

	_ = enc.Encode(w, eventstream.Message{Headers: h, Payload: payload})
}
