// Package awseventstream writes AWS event-stream responses
// (application/vnd.amazon.eventstream): JSON events and modeled exceptions,
// each flushed as it is written, using the SDK's own frame encoder.
package awseventstream

import (
	"errors"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
)

const (
	// ContentType is the response Content-Type of an event stream.
	ContentType     = "application/vnd.amazon.eventstream"
	contentTypeJSON = "application/json"
	// InitialResponse is the event type of the first frame of an AWS JSON
	// event stream. SDKs block the operation call until it arrives.
	InitialResponse = "initial-response"
)

// Writer frames events onto one HTTP response.
type Writer struct {
	w   http.ResponseWriter
	rc  *http.ResponseController
	enc *eventstream.Encoder
}

// Start writes the 200 event-stream response header and returns a Writer
// for its frames.
func Start(w http.ResponseWriter) *Writer {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(http.StatusOK)

	return &Writer{w: w, rc: http.NewResponseController(w), enc: eventstream.NewEncoder()}
}

// Event writes one JSON event of the given :event-type and flushes it. The
// error is non-nil once the client has gone away.
func (s *Writer) Event(eventType string, payload []byte) error {
	var hdr eventstream.Headers

	hdr.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.EventMessageType))
	hdr.Set(eventstreamapi.EventTypeHeader, eventstream.StringValue(eventType))
	hdr.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	return s.write(hdr, payload)
}

// Exception writes one modeled exception of the given :exception-type, with
// a JSON payload, and flushes it.
func (s *Writer) Exception(exceptionType string, payload []byte) error {
	var hdr eventstream.Headers

	hdr.Set(eventstreamapi.MessageTypeHeader, eventstream.StringValue(eventstreamapi.ExceptionMessageType))
	hdr.Set(eventstreamapi.ExceptionTypeHeader, eventstream.StringValue(exceptionType))
	hdr.Set(eventstreamapi.ContentTypeHeader, eventstream.StringValue(contentTypeJSON))

	return s.write(hdr, payload)
}

func (s *Writer) write(hdr eventstream.Headers, payload []byte) error {
	if err := s.enc.Encode(s.w, eventstream.Message{Headers: hdr, Payload: payload}); err != nil {
		return err
	}

	// A writer without flush support still delivers the frames when the
	// response ends.
	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}

	return nil
}
