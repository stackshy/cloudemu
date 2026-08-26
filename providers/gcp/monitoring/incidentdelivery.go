package monitoring

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// webhookDeliveryTimeout bounds how long a single incident webhook POST waits
// for the receiver so an unreachable channel cannot stall the breaching
// PutMetricData indefinitely. It mirrors azure/monitor's webhookDeliveryTimeout.
const webhookDeliveryTimeout = 10 * time.Second

// WebhookDeliverer delivers an incident notification to a webhook channel's URL.
// New() defaults it to a real-HTTP implementation (httpWebhookDeliverer), so a
// breach that targets a webhook notification channel performs the real POST in
// production — mirroring azure/monitor's WebhookDeliverer. SetWebhookDeliverer
// is a test seam that swaps in a fake so a test can assert delivery without a
// live receiver.
type WebhookDeliverer interface {
	Deliver(ctx context.Context, url, payload string) error
}

// PubSubPublisher publishes an incident to a Pub/Sub notification channel's
// topic. Pub/Sub topic fanout (topic -> subscriptions) lives in the wire
// handler, not the SQS-shaped message-queue driver, so this is wired at the
// server layer (server/gcp/gcp.go) with an adapter over the Pub/Sub handler,
// mirroring where #803 wired the Pub/Sub function-invoker. Nil (the library
// path, which has no topic fanout) leaves Pub/Sub delivery record-only.
type PubSubPublisher interface {
	PublishIncident(ctx context.Context, topic string, data []byte)
}

// httpWebhookDeliverer is the production WebhookDeliverer: a best-effort real
// HTTP POST of the incident payload to a webhook channel's URL. It mirrors
// azure/monitor's httpWebhookDeliverer — a bounded http.Client, and errors are
// surfaced to the caller (deliverWebhook), which swallows them so a breach /
// PutMetricData never fails because a receiver is unreachable.
type httpWebhookDeliverer struct {
	client *http.Client
}

// newHTTPWebhookDeliverer builds the default deliverer with a bounded client,
// matching azure/monitor's http.Client{Timeout: webhookDeliveryTimeout}.
func newHTTPWebhookDeliverer() *httpWebhookDeliverer {
	return &httpWebhookDeliverer{client: &http.Client{Timeout: webhookDeliveryTimeout}}
}

// Deliver POSTs the payload to url as the incident body, with the JSON
// content-type real Cloud Monitoring webhook channels carry.
func (d *httpWebhookDeliverer) Deliver(ctx context.Context, url, payload string) error {
	reqCtx, cancel := context.WithTimeout(ctx, webhookDeliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, strings.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	return nil
}

// SetWebhookDeliverer overrides the webhook deliverer. It is a test seam: tests
// inject a fake to observe delivery. New() already installs a real-HTTP default,
// so production never calls this. Nil leaves webhook delivery record-only.
func (m *Mock) SetWebhookDeliverer(d WebhookDeliverer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.webhookDeliverer = d
}

// SetPubSubPublisher wires the Pub/Sub publisher so a breach that targets a
// pubsub notification channel publishes the incident to the channel's topic.
// It is wired at the server layer because topic fanout is wire-only; the
// library path leaves it nil (record-only).
func (m *Mock) SetPubSubPublisher(p PubSubPublisher) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pubsubPublisher = p
}

// incidentEnvelope is the Cloud Monitoring notification body delivered to a
// channel: a reasonable subset of the real incident schema, identical for the
// webhook POST body and the Pub/Sub message data.
type incidentEnvelope struct {
	Incident incidentBody `json:"incident"`
}

type incidentBody struct {
	IncidentID    string `json:"incident_id"`
	ResourceID    string `json:"resource_id"`
	PolicyName    string `json:"policy_name"`
	ConditionName string `json:"condition_name"`
	State         string `json:"state"` // open | closed
	StartedAt     int64  `json:"started_at"`
	Summary       string `json:"summary"`
	URL           string `json:"url"`
}

// fireNotificationChannels delivers an alert policy's incident to each of its
// referenced notification channels on an incident open (ALARM) or close (OK)
// transition — real Cloud Monitoring notifies on both by default. Webhook
// channels are POSTed; pubsub channels are published to their topic; email /
// SMS / other channels are record-only (the emulator cannot send them). All
// delivery is best-effort so a breach never fails on an unreachable channel.
func (m *Mock) fireNotificationChannels(alarm *alarmData, newState string, now time.Time) {
	if newState != alarmeval.StateAlarm && newState != alarmeval.StateOK {
		return
	}

	body, err := json.Marshal(buildIncident(alarm, newState, now))
	if err != nil {
		return
	}

	m.mu.RLock()
	webhook := m.webhookDeliverer
	publisher := m.pubsubPublisher
	m.mu.RUnlock()

	for _, ref := range alarm.AlarmActions {
		if ch := m.lookupChannel(ref); ch != nil {
			deliverIncident(ch, webhook, publisher, body)
		}
	}
}

// deliverIncident delivers one incident to a single channel by type: webhook
// channels are POSTed and pubsub channels are published; email / SMS / other
// channels are record-only (the transition history is their record, and the
// emulator cannot deliver them externally). Delivery is best-effort.
func deliverIncident(
	ch *driver.NotificationChannelInfo, webhook WebhookDeliverer, publisher PubSubPublisher, body []byte,
) {
	if ch.Endpoint == "" {
		return
	}

	switch {
	case isWebhookChannel(ch.Type):
		if webhook != nil {
			_ = webhook.Deliver(context.Background(), ch.Endpoint, string(body))
		}
	case ch.Type == channelTypePubSub:
		if publisher != nil {
			publisher.PublishIncident(context.Background(), ch.Endpoint, body)
		}
	}
}

const channelTypePubSub = "pubsub"

// isWebhookChannel reports whether a channel type is one of Cloud Monitoring's
// webhook variants (webhook_tokenauth / webhook_basicauth), or a bare "webhook".
func isWebhookChannel(channelType string) bool {
	return strings.Contains(channelType, "webhook")
}

// lookupChannel resolves an AlarmActions reference — a channel resource name
// (projects/P/notificationChannels/ID) or a bare channel ID — to its stored
// channel, or nil when it names no known channel.
func (m *Mock) lookupChannel(ref string) *driver.NotificationChannelInfo {
	ch, ok := m.channels.Get(lastSegment(ref))
	if !ok {
		return nil
	}

	return ch
}

// lastSegment returns the final path segment of a resource reference.
func lastSegment(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}

	return ref
}

// buildIncident renders the incident envelope for a transition.
func buildIncident(alarm *alarmData, newState string, now time.Time) incidentEnvelope {
	id := idgen.GenerateID("incident-")
	state := "open"

	if newState == alarmeval.StateOK {
		state = "closed"
	}

	return incidentEnvelope{Incident: incidentBody{
		IncidentID:    id,
		ResourceID:    incidentResourceID(alarm.Dimensions),
		PolicyName:    alarm.Name,
		ConditionName: alarm.MetricName,
		State:         state,
		StartedAt:     now.Unix(),
		Summary:       alarm.Name + " is " + state + ": " + alarm.StateReason,
		URL:           "https://console.cloud.google.com/monitoring/alerting/incidents/" + id,
	}}
}

// incidentResourceID renders a deterministic resource id from an alert policy's
// dimensions (the monitored resource the condition filters on).
func incidentResourceID(dims map[string]string) string {
	if len(dims) == 0 {
		return ""
	}

	keys := make([]string, 0, len(dims))
	for k := range dims {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+dims[k])
	}

	return strings.Join(parts, ",")
}
