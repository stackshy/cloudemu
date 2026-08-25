package eventgrid

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// publishPath is the data-plane publish path a topic endpoint exposes:
// https://{topic}.{region}-1.eventgrid.azure.net/api/events.
const publishPath = "/api/events"

// PublishHandler serves the Event Grid data-plane publish endpoint. The topic
// is taken from the request Host's leftmost label ({topic}.{region}-1.
// eventgrid.azure.net), matching how a real publisher addresses a topic.
type PublishHandler struct {
	bus ebdriver.EventBus
}

// NewPublishHandler returns the Event Grid data-plane publish handler backed by b.
func NewPublishHandler(b ebdriver.EventBus) *PublishHandler {
	return &PublishHandler{bus: b}
}

// Matches claims POST requests to the /api/events publish path.
func (*PublishHandler) Matches(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == publishPath
}

// eventGridEvent is the EventGridEvent schema a publisher posts.
type eventGridEvent struct {
	ID          string          `json:"id"`
	Topic       string          `json:"topic"`
	Subject     string          `json:"subject"`
	EventType   string          `json:"eventType"`
	EventTime   string          `json:"eventTime"`
	Data        json.RawMessage `json:"data"`
	DataVersion string          `json:"dataVersion"`
}

// topicFromHost extracts the topic name from an Event Grid topic host of the
// form {topic}.{region}-1.eventgrid.azure.net.
func topicFromHost(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}

	label, _, ok := strings.Cut(host, ".")
	if !ok {
		return ""
	}

	if !strings.Contains(host, "eventgrid") {
		return ""
	}

	return label
}

// ServeHTTP decodes the EventGridEvent array and publishes it to the topic
// named by the request Host.
func (h *PublishHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	topic := topicFromHost(r.Host)
	if topic == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequest",
			"publish host must be {topic}.{region}-1.eventgrid.azure.net")

		return
	}

	ctx := withDeliveryDepth(r)

	if _, err := h.bus.GetEventBus(ctx, topic); err != nil {
		azurearm.WriteError(w, http.StatusNotFound, "TopicNotFound", "topic "+topic+" not found")
		return
	}

	var events []eventGridEvent
	if !azurearm.DecodeJSON(w, r, &events) {
		return
	}

	drvEvents := make([]ebdriver.Event, 0, len(events))
	for i := range events {
		drvEvents = append(drvEvents, ebdriver.Event{
			ID:         events[i].ID,
			Source:     topic,
			DetailType: events[i].EventType,
			Detail:     string(events[i].Data),
			Time:       parseEventTime(events[i].EventTime),
			EventBus:   topic,
			Subject:    events[i].Subject,
		})
	}

	if _, err := h.bus.PutEvents(ctx, drvEvents); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// withDeliveryDepth seeds the request context with the re-entrant delivery
// depth carried on recursionguard.DepthHeader. A WebHook subscription whose
// endpointUrl points back here re-enters this handler over HTTP, where the
// in-process depth can't ride the goroutine; reading it off the header keeps a
// self-referential publish->deliver->publish chain counting toward the cap.
func withDeliveryDepth(r *http.Request) context.Context {
	ctx := r.Context()
	if d, err := strconv.Atoi(r.Header.Get(recursionguard.DepthHeader)); err == nil && d > 0 {
		ctx = recursionguard.WithDepth(ctx, d)
	}

	return ctx
}

func parseEventTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}

	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}

	return time.Time{}
}
