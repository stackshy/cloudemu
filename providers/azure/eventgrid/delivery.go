package eventgrid

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// ARM EventSubscriptionDestination.endpointType values this mock delivers to.
// WebHook POSTs the event envelope over HTTP; ServiceBusQueue/ServiceBusTopic
// enqueue it into the peer Service Bus queue/topic; AzureFunction invokes the
// peer Functions app with it. EventHub, StorageQueue, and HybridConnection
// destinations are still parsed and round-trip on the subscription resource
// (the raw ARM properties JSON is echoed verbatim regardless of type) but are
// not delivered to: they have no peer mock in this codebase and are left as a
// follow-up (see PR notes).
const (
	endpointTypeWebHook         = "WebHook"
	endpointTypeServiceBusQueue = "ServiceBusQueue"
	endpointTypeServiceBusTopic = "ServiceBusTopic"
	endpointTypeAzureFunction   = "AzureFunction"
)

// defaultDataVersion is the dataVersion Event Grid stamps on a delivered event
// when the publisher omitted one; metadataVersion is the read-only schema
// version Event Grid always reports as "1".
const (
	defaultDataVersion = "1.0"
	metadataVersion    = "1"
)

// dataVersionOrDefault returns the publisher's dataVersion, defaulting to "1.0"
// only when the publisher omitted it (matching real Event Grid).
func dataVersionOrDefault(v string) string {
	if v == "" {
		return defaultDataVersion
	}

	return v
}

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

// deliverToTargets delivers event to every matched subscription's destination,
// dispatched by the destination's endpointType: WebHook (HTTP POST),
// ServiceBusQueue/ServiceBusTopic (enqueue into the peer Service Bus), and
// AzureFunction (invoke the peer Functions app). All destinations receive the
// same rendered EventGridEvent envelope. Delivery is synchronous (mirroring how
// AWS EventBridge's deliverToTargets runs in-line in this codebase) but
// decoupled from the caller's context so a client that cancels its publish
// request doesn't abort in-flight deliveries. The caller ctx's re-entrant
// delivery depth is carried forward so a self-referential chain (a WebHook that
// points back at this emulator, or a Function that re-publishes) stays bounded —
// the cap is enforced in postWebhook / functions.InvokeExternal, mirroring
// lambda.InvokeExternal. EventHub, StorageQueue, and HybridConnection
// destinations are parsed and round-trip on the subscription resource but are
// not delivered to (no peer mock; see PR notes). Filters are already applied by
// matchedRuleData, so every rd here is a filter-matched subscription.
func (m *Mock) deliverToTargets(ctx context.Context, matched []*ruleData, event *driver.Event, topicARN string) {
	if len(matched) == 0 {
		return
	}

	// Decouple delivery from the caller's cancellation/deadline while carrying
	// the re-entrant delivery depth forward onto a background-rooted context.
	dctx := recursionguard.WithDepth(context.Background(), recursionguard.Depth(ctx))

	// A system-topic producer stamps the source resource id on event.Topic (real
	// Azure delivers the storage account id, not the Event Grid topic resource);
	// a custom-topic publish leaves it empty and delivery reports the bus's ARN.
	topic := event.Topic
	if topic == "" {
		topic = topicARN
	}

	payload := deliveryEvent{
		ID:              event.ID,
		Topic:           topic,
		Subject:         event.Subject,
		EventType:       event.DetailType,
		EventTime:       event.Time.UTC().Format(time.RFC3339Nano),
		Data:            eventDataOrEmpty(event.Detail),
		DataVersion:     dataVersionOrDefault(event.DataVersion),
		MetadataVersion: metadataVersion,
	}

	body, err := json.Marshal([]deliveryEvent{payload})
	if err != nil {
		return
	}

	for _, rd := range matched {
		m.dispatchDestination(dctx, rd.dest, body)
	}
}

// dispatchDestination routes one rendered envelope to a single subscription
// destination by its endpointType. A nil peer (the injector was never wired) or
// an unresolvable resource id is skipped gracefully — never a panic — matching
// how EventBridge's dispatchTarget silently ignores an unwired sink.
func (m *Mock) dispatchDestination(ctx context.Context, dest subscriptionDestination, body []byte) {
	switch dest.EndpointType {
	case endpointTypeWebHook:
		if dest.EndpointURL != "" {
			m.postWebhook(ctx, dest.EndpointURL, body)
		}
	case endpointTypeServiceBusQueue, endpointTypeServiceBusTopic:
		if m.serviceBus == nil {
			return
		}

		if name := resourceLeafName(dest.ResourceID); name != "" {
			_ = m.serviceBus.DeliverExternal(ctx, name, string(body))
		}
	case endpointTypeAzureFunction:
		if m.functions == nil {
			return
		}

		if app := functionAppName(dest.ResourceID); app != "" {
			_ = m.functions.InvokeExternal(ctx, app, body)
		}
	}
}

// resourceLeafName returns the trailing path segment of an ARM resource id — the
// Service Bus queue or topic name in a ServiceBusQueue/ServiceBusTopic
// destination's resourceId (.../namespaces/<ns>/queues/<q> or .../topics/<t>).
func resourceLeafName(resourceID string) string {
	trimmed := strings.TrimRight(resourceID, "/")
	if trimmed == "" {
		return ""
	}

	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}

	return trimmed
}

// functionAppName returns the function-app (site) name from an AzureFunction
// destination's resourceId (.../Microsoft.Web/sites/<app>/functions/<fn>). The
// Functions mock is keyed by the app name (its Microsoft.Web/sites resource), so
// the app segment — not the trailing <fn> — is what resolves the peer.
func functionAppName(resourceID string) string {
	const marker = "/sites/"

	i := strings.Index(resourceID, marker)
	if i < 0 {
		return ""
	}

	rest := resourceID[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}

	return rest
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
