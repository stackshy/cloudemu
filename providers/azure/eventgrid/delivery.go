package eventgrid

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// endpointTypeWebHook is the only ARM EventSubscriptionDestination.endpointType
// this mock actually delivers to (see deliverToTargets). ServiceBusQueue,
// ServiceBusTopic, EventHub, AzureFunction, StorageQueue, and HybridConnection
// destinations are parsed and round-trip on the subscription resource (the raw
// ARM properties JSON is echoed verbatim regardless of type) but are not
// delivered to: EventHub has no peer mock in this codebase, and wiring
// ServiceBus/Functions delivery is left as a follow-up (see PR notes).
const endpointTypeWebHook = "WebHook"

// webhookDeliveryTimeout bounds how long PutEvents waits for a single WebHook
// destination to respond. Real Event Grid waits up to 30s before queuing a
// retry; this mock uses a shorter bound so a dead test endpoint can't hang a
// publish call indefinitely.
const webhookDeliveryTimeout = 10 * time.Second

// subscriptionDestination is the parsed form of an ARM
// EventSubscriptionDestination (properties.destination on an event
// subscription) — the polymorphic "endpointType" + nested "properties" union
// ARM emits. Azure-only, so it stays local to this provider package rather
// than the shared eventbus driver.
type subscriptionDestination struct {
	EndpointType string
	EndpointURL  string // WebHook
	ResourceID   string // ServiceBusQueue/Topic, EventHub, AzureFunction, StorageQueue, HybridConnection
}

// parseSubscriptionDestination extracts the "destination" object from a raw
// ARM EventSubscription properties JSON blob.
func parseSubscriptionDestination(rawProperties string) subscriptionDestination {
	if rawProperties == "" {
		return subscriptionDestination{}
	}

	var body struct {
		Destination struct {
			EndpointType string `json:"endpointType"`
			Properties   struct {
				EndpointURL string `json:"endpointUrl"`
				ResourceID  string `json:"resourceId"`
			} `json:"properties"`
		} `json:"destination"`
	}

	if err := json.Unmarshal([]byte(rawProperties), &body); err != nil {
		return subscriptionDestination{}
	}

	return subscriptionDestination{
		EndpointType: body.Destination.EndpointType,
		EndpointURL:  body.Destination.Properties.EndpointURL,
		ResourceID:   body.Destination.Properties.ResourceID,
	}
}

// deliveryEvent is the Event Grid schema payload POSTed to a WebHook
// destination. Real Event Grid defaults to unbatched delivery — one event per
// request, carried as a single-element array — which this mirrors.
type deliveryEvent struct {
	ID              string          `json:"id"`
	Topic           string          `json:"topic"`
	Subject         string          `json:"subject"`
	EventType       string          `json:"eventType"`
	EventTime       string          `json:"eventTime"`
	Data            json.RawMessage `json:"data"`
	DataVersion     string          `json:"dataVersion"`
	MetadataVersion string          `json:"metadataVersion"`
}

// deliverToTargets POSTs event to every matched subscription's WebHook
// destination. Delivery is synchronous (mirroring how AWS EventBridge's
// deliverToTargets runs in-line in this codebase) but decoupled from the
// caller's context so a client that cancels its publish request doesn't abort
// in-flight deliveries. The caller ctx's re-entrant delivery depth is carried
// forward so a self-referential WebHook chain (a subscription endpointUrl that
// points back at this emulator's publish endpoint) stays bounded — the cap is
// enforced in postWebhook, mirroring lambda.InvokeExternal. ServiceBusQueue/
// Topic, EventHub, and AzureFunction destinations are parsed and round-trip on
// the subscription resource, but are not delivered to: EventHub has no peer
// mock in this codebase, and wiring ServiceBus/Functions delivery is left as a
// follow-up (see PR notes).
func (m *Mock) deliverToTargets(ctx context.Context, matched []*ruleData, event *driver.Event, topicARN string) {
	if len(matched) == 0 {
		return
	}

	// Decouple delivery from the caller's cancellation/deadline while carrying
	// the re-entrant delivery depth forward onto a background-rooted context.
	dctx := recursionguard.WithDepth(context.Background(), recursionguard.Depth(ctx))

	payload := deliveryEvent{
		ID:          event.ID,
		Topic:       topicARN,
		Subject:     event.Subject,
		EventType:   event.DetailType,
		EventTime:   event.Time.UTC().Format(time.RFC3339Nano),
		Data:        eventDataOrEmpty(event.Detail),
		DataVersion: "1.0",
	}

	body, err := json.Marshal([]deliveryEvent{payload})
	if err != nil {
		return
	}

	for _, rd := range matched {
		if rd.dest.EndpointType != endpointTypeWebHook || rd.dest.EndpointURL == "" {
			continue
		}

		m.postWebhook(dctx, rd.dest.EndpointURL, body)
	}
}

// postWebhook delivers one rendered batch to a WebHook endpoint, matching the
// headers real Event Grid sends. Errors (unreachable endpoint, non-2xx
// status) are swallowed: this mock does not model Event Grid's retry/dead-
// letter pipeline, so a failed delivery is simply dropped, same as
// EventBridge's dispatchTarget in this codebase.
//
// ctx carries the re-entrant delivery depth (see internal/recursionguard). A
// subscription's endpointUrl is arbitrary ARM input and can point back at this
// emulator's own publish endpoint; because delivery is synchronous, an
// unbounded self-referential chain would tie up one blocked goroutine per
// level. Once the depth reaches recursionguard.MaxDepth — mirroring
// lambda.InvokeExternal — the delivery is dropped instead of recursing further.
func (m *Mock) postWebhook(ctx context.Context, url string, body []byte) {
	depth := recursionguard.Depth(ctx)
	if depth >= recursionguard.MaxDepth {
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, webhookDeliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Aeg-Event-Type", "Notification")
	// Carry the incremented depth across the webhook round-trip so a publish
	// endpoint that re-enters PutEvents keeps counting toward the cap.
	req.Header.Set(recursionguard.DepthHeader, strconv.Itoa(depth+1))

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)
}

func eventDataOrEmpty(detail string) json.RawMessage {
	if detail == "" {
		return json.RawMessage("{}")
	}

	return json.RawMessage(detail)
}
