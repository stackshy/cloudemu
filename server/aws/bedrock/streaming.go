package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

const (
	contentTypeEventStream = "application/vnd.amazon.eventstream"
	keyContentBlockIndex   = "contentBlockIndex"
	// minSplitLen is the shortest text worth splitting across two deltas.
	minSplitLen = 2
)

// eventWriter frames Bedrock runtime streaming responses as
// application/vnd.amazon.eventstream events, flushing each event as it is
// written so SDK clients observe an incremental stream.
type eventWriter struct {
	w       http.ResponseWriter
	enc     *eventstream.Encoder
	flusher http.Flusher
}

// newEventWriter sets the eventstream content type, writes a 200 status, and
// returns a writer ready to emit events. Call only after the driver result is
// known so errors can still be reported via writeErr.
func newEventWriter(w http.ResponseWriter) *eventWriter {
	w.Header().Set("Content-Type", contentTypeEventStream)
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	return &eventWriter{w: w, enc: eventstream.NewEncoder(), flusher: flusher}
}

// event encodes one JSON event of the given type and flushes it.
func (e *eventWriter) event(eventType string, payload []byte) {
	var h eventstream.Headers

	h.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
	h.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue(eventType))
	h.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	_ = e.enc.Encode(e.w, eventstream.Message{Headers: h, Payload: payload})

	if e.flusher != nil {
		e.flusher.Flush()
	}
}

// converseStream handles POST /model/{modelId}/converse-stream. It reuses the
// Converse driver call and frames the full result as a sequence of streaming
// events (messageStart, contentBlockDelta(s), contentBlockStop, messageStop,
// metadata).
func (h *Handler) converseStream(w http.ResponseWriter, r *http.Request, modelID string) {
	var in converseRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	// Drain any bytes the JSON decoder left unread (e.g. a trailing newline)
	// before switching to a streamed chunked response. With the request body
	// unread, net/http can't finish the connection gracefully and tears it down
	// when the handler returns, which under load races the client's in-flight
	// read of the event stream and surfaces as "use of closed network
	// connection". invokeModelStream already reads the whole body via io.ReadAll.
	_, _ = io.Copy(io.Discard, r.Body)

	out, err := h.bedrock.Converse(r.Context(), toConverseInput(modelID, &in))
	if err != nil {
		writeErr(w, err)

		return
	}

	ev := newEventWriter(w)
	ev.event("messageStart", mustJSON(map[string]string{"role": out.Message.Role}))

	for _, chunk := range chunkText(strings.Join(out.Message.Text, "")) {
		ev.event("contentBlockDelta", mustJSON(map[string]any{
			keyContentBlockIndex: 0,
			"delta":              map[string]string{"text": chunk},
		}))
	}

	ev.event("contentBlockStop", mustJSON(map[string]any{keyContentBlockIndex: 0}))
	ev.event("messageStop", mustJSON(map[string]string{"stopReason": out.StopReason}))
	ev.event("metadata", metadataPayload(out))
}

// invokeModelStream handles POST /model/{modelId}/invoke-with-response-stream.
// It reuses the InvokeModel driver call and emits the model-native response as
// a single base64-encoded chunk event.
func (h *Handler) invokeModelStream(w http.ResponseWriter, r *http.Request, modelID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "read body: "+err.Error())

		return
	}

	res, err := h.bedrock.InvokeModel(r.Context(), bedrockdriver.InvokeModelInput{
		ModelID:     modelID,
		ContentType: r.Header.Get("Content-Type"),
		Accept:      r.Header.Get("Accept"),
		Body:        body,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	ev := newEventWriter(w)
	ev.event("chunk", mustJSON(map[string]string{
		"bytes": base64.StdEncoding.EncodeToString(res.Body),
	}))
}

// metadataPayload builds the Converse metadata event carrying token usage and
// latency metrics.
func metadataPayload(out *bedrockdriver.ConverseOutput) []byte {
	return mustJSON(map[string]any{
		"usage": map[string]int{
			"inputTokens":  out.InputTokens,
			"outputTokens": out.OutputTokens,
			"totalTokens":  out.TotalTokens,
		},
		"metrics": map[string]int{"latencyMs": out.LatencyMs},
	})
}

// chunkText splits s into up to two contentBlockDelta chunks so the emulated
// stream delivers more than one delta when the text is long enough. It always
// returns at least one chunk.
//
// The split point is advanced to a UTF-8 rune boundary so neither half can
// contain a truncated multi-byte rune. Splitting on a raw byte offset would
// leave both halves as invalid UTF-8, which encoding/json then marshals as the
// U+FFFD replacement character, corrupting the streamed text.
func chunkText(s string) []string {
	if len(s) < minSplitLen {
		return []string{s}
	}

	mid := len(s) / 2
	for mid < len(s) && !utf8.RuneStart(s[mid]) {
		mid++
	}

	if mid == 0 || mid >= len(s) {
		return []string{s}
	}

	return []string{s[:mid], s[mid:]}
}

// mustJSON marshals v to JSON, returning an empty object on the impossible
// error path for these fixed, marshalable payload shapes.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}

	return b
}
