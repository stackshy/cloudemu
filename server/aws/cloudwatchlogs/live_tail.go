package cloudwatchlogs

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/server/wire/awseventstream"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Live Tail event-stream event and exception types.
const (
	eventSessionStart       = "sessionStart"
	eventSessionUpdate      = "sessionUpdate"
	sessionTimeoutException = "SessionTimeoutException"
	sessionTimeoutMessage   = "The Live Tail session has timed out after three hours."
)

type startLiveTailRequest struct {
	LogGroupIdentifiers   []string `json:"logGroupIdentifiers"`
	LogStreamNames        []string `json:"logStreamNames"`
	LogStreamNamePrefixes []string `json:"logStreamNamePrefixes"`
	LogEventFilterPattern string   `json:"logEventFilterPattern"`
}

type liveTailSessionStartJSON struct {
	RequestID             string   `json:"requestId"`
	SessionID             string   `json:"sessionId"`
	LogGroupIdentifiers   []string `json:"logGroupIdentifiers"`
	LogStreamNames        []string `json:"logStreamNames,omitempty"`
	LogStreamNamePrefixes []string `json:"logStreamNamePrefixes,omitempty"`
	LogEventFilterPattern string   `json:"logEventFilterPattern,omitempty"`
}

type liveTailLogEventJSON struct {
	LogGroupIdentifier string `json:"logGroupIdentifier"`
	LogStreamName      string `json:"logStreamName"`
	Message            string `json:"message"`
	Timestamp          int64  `json:"timestamp"`
	IngestionTime      int64  `json:"ingestionTime"`
}

type liveTailExceptionJSON struct {
	Message string `json:"message"`
}

type liveTailSessionUpdateJSON struct {
	SessionMetadata struct {
		Sampled bool `json:"sampled"`
	} `json:"sessionMetadata"`
	SessionResults []liveTailLogEventJSON `json:"sessionResults"`
}

// startLiveTail handles StartLiveTail. Request errors are ordinary JSON
// errors. Once the session is open the response is an event stream: the
// initial-response, a sessionStart event, then the session's updates until
// the client disconnects, the server shuts down, or the session times out.
func (h *Handler) startLiveTail(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "StartLiveTail")
	if !ok {
		return
	}

	var req startLiveTailRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ctx, cancel := wire.StreamContext(r.Context())
	defer cancel()

	sess, err := ext.StartLiveTail(ctx, &logdriver.StartLiveTailInput{
		LogGroupIdentifiers:   req.LogGroupIdentifiers,
		LogStreamNames:        req.LogStreamNames,
		LogStreamNamePrefixes: req.LogStreamNamePrefixes,
		LogEventFilterPattern: req.LogEventFilterPattern,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	defer sess.Close()

	stream := awseventstream.Start(w)

	if stream.Event(awseventstream.InitialResponse, []byte("{}")) != nil {
		return
	}

	start, _ := json.Marshal(liveTailSessionStartJSON{
		RequestID:             idgen.UUID(),
		SessionID:             sess.SessionID(),
		LogGroupIdentifiers:   req.LogGroupIdentifiers,
		LogStreamNames:        req.LogStreamNames,
		LogStreamNamePrefixes: req.LogStreamNamePrefixes,
		LogEventFilterPattern: req.LogEventFilterPattern,
	})
	if stream.Event(eventSessionStart, start) != nil {
		return
	}

	streamLiveTail(stream, sess)
}

// streamLiveTail writes each update until the session ends or a write fails.
// A timed-out session ends with SessionTimeoutException.
func streamLiveTail(stream *awseventstream.Writer, sess logdriver.LiveTailSession) {
	for u := range sess.Updates() {
		payload, _ := json.Marshal(toLiveTailUpdate(u))
		if stream.Event(eventSessionUpdate, payload) != nil {
			return
		}
	}

	if sess.TimedOut() {
		payload, _ := json.Marshal(liveTailExceptionJSON{Message: sessionTimeoutMessage})
		_ = stream.Exception(sessionTimeoutException, payload)
	}
}

func toLiveTailUpdate(u logdriver.LiveTailUpdate) liveTailSessionUpdateJSON {
	var out liveTailSessionUpdateJSON

	out.SessionMetadata.Sampled = u.Sampled
	out.SessionResults = make([]liveTailLogEventJSON, 0, len(u.Results))

	for _, e := range u.Results {
		out.SessionResults = append(out.SessionResults, liveTailLogEventJSON{
			LogGroupIdentifier: e.LogGroupIdentifier,
			LogStreamName:      e.LogStreamName,
			Message:            e.Message,
			Timestamp:          epochMillis(e.Timestamp),
			IngestionTime:      epochMillis(e.IngestionTime),
		})
	}

	return out
}
