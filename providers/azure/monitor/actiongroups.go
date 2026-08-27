package monitor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// Receiver type discriminators recorded on a delivery.
const (
	receiverEmail         = "email"
	receiverWebhook       = "webhook"
	receiverSMS           = "sms"
	receiverAzureFunction = "azureFunction"
)

// webhookDeliveryTimeout bounds how long a single webhook POST waits for the
// receiver to respond, so a dead endpoint can't hang an alarm evaluation /
// PutMetricData indefinitely. It mirrors eventgrid's webhookDeliveryTimeout.
const webhookDeliveryTimeout = 10 * time.Second

// ActionGroupReceiver is one resolved receiver of an action group. Endpoint is
// the receiver's address: the email address, webhook URI, phone number, or
// Azure Function trigger URL, depending on Type.
type ActionGroupReceiver struct {
	Type     string
	Name     string
	Endpoint string
}

// ActionGroupDelivery records one action-group receiver fired by an alert
// breach. It is the observable side effect that lets a caller (or a test)
// assert an OK->ALARM transition actually reached the action group's receivers,
// mirroring how an AWS CloudWatch alarm publishes to its SNS topic subscribers.
type ActionGroupDelivery struct {
	ActionGroupID string
	ReceiverType  string
	ReceiverName  string
	Endpoint      string
	AlarmName     string
	NewState      string
	Timestamp     time.Time
}

// actionGroupData is the stored, evaluation-side view of an action group: its
// resource id and the receivers a breach delivers to.
type actionGroupData struct {
	ID        string
	Enabled   bool
	Receivers []ActionGroupReceiver
}

// WebhookDeliverer delivers an alert notification to a webhook receiver's URI.
// New() defaults it to a real-HTTP implementation (httpWebhookDeliverer), so a
// breach that targets an action group with webhook receivers performs the real
// POST in production — mirroring how eventgrid.New wires a real httpClient and
// POSTs to WebHook destinations. SetWebhookDeliverer is a test seam that swaps
// in a fake so a test can assert delivery without a live receiver.
type WebhookDeliverer interface {
	Deliver(ctx context.Context, uri, payload string) error
}

// httpWebhookDeliverer is the production WebhookDeliverer: a best-effort real
// HTTP POST of the alert payload to a webhook receiver's URI. It mirrors
// eventgrid.Mock's postWebhook — a bounded http.Client, and errors are surfaced
// to the caller (deliverWebhook), which swallows them so a breach / PutMetricData
// never fails because a receiver is unreachable.
type httpWebhookDeliverer struct {
	client *http.Client
}

// newHTTPWebhookDeliverer builds the default deliverer with a bounded client,
// matching eventgrid.New's http.Client{Timeout: webhookDeliveryTimeout}.
func newHTTPWebhookDeliverer() *httpWebhookDeliverer {
	return &httpWebhookDeliverer{client: &http.Client{Timeout: webhookDeliveryTimeout}}
}

// Deliver POSTs the payload to uri as the action-group webhook body, with the
// JSON content-type real Azure action-group webhooks carry.
func (d *httpWebhookDeliverer) Deliver(ctx context.Context, uri, payload string) error {
	reqCtx, cancel := context.WithTimeout(ctx, webhookDeliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, uri, strings.NewReader(payload))
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

// RegisterActionGroup registers (or replaces) an action group's receivers under
// its ARM resource id so an alert that names the id in its actions delivers to
// the receivers on a breach. The wire handler calls this on an actionGroups
// create/update; the id is stored case-insensitively because a metric alert may
// reference it with different casing than it was created under.
func (m *Mock) RegisterActionGroup(id string, properties map[string]any) {
	m.actionGroups.Set(strings.ToLower(id), &actionGroupData{
		ID:        id,
		Enabled:   actionGroupEnabled(properties),
		Receivers: parseActionGroupReceivers(properties),
	})
}

// UnregisterActionGroup removes an action group so its receivers stop being
// delivered to. The wire handler calls this on an actionGroups delete.
func (m *Mock) UnregisterActionGroup(id string) {
	m.actionGroups.Delete(strings.ToLower(id))
}

// ActionGroupDeliveries returns a snapshot of every action-group receiver
// delivery fired so far, in delivery order.
func (m *Mock) ActionGroupDeliveries() []ActionGroupDelivery {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return append([]ActionGroupDelivery{}, m.deliveries...)
}

// fireActionGroups delivers an alarm's action groups on a transition into
// ALARM: each AlarmActions id is resolved against the registered action groups
// and every receiver is recorded (and, for webhook receivers, POSTed by the
// deliverer). A disabled action group delivers to none of its receivers.
func (m *Mock) fireActionGroups(alarm *alarmData, newState string, now time.Time) {
	for _, agID := range alarm.AlarmActions {
		ag, ok := m.actionGroups.Get(strings.ToLower(agID))
		if !ok || !ag.Enabled {
			continue
		}

		for _, rcv := range ag.Receivers {
			m.recordDelivery(&ActionGroupDelivery{
				ActionGroupID: ag.ID,
				ReceiverType:  rcv.Type,
				ReceiverName:  rcv.Name,
				Endpoint:      rcv.Endpoint,
				AlarmName:     alarm.Name,
				NewState:      newState,
				Timestamp:     now,
			})

			m.deliverWebhook(rcv, alarm, newState, now)
		}
	}
}

// deliverWebhook performs the best-effort HTTP POST for a webhook receiver
// using the wired deliverer (a real-HTTP one by default from New); it is a
// no-op for non-webhook receivers, or when a test cleared the deliverer to nil.
func (m *Mock) deliverWebhook(rcv ActionGroupReceiver, alarm *alarmData, newState string, now time.Time) {
	if rcv.Type != receiverWebhook || rcv.Endpoint == "" {
		return
	}

	m.mu.RLock()
	deliverer := m.webhookDeliverer
	m.mu.RUnlock()

	if deliverer == nil {
		return
	}

	_ = deliverer.Deliver(context.Background(), rcv.Endpoint, alarmPayload(alarm, newState, now))
}

// recordDelivery appends one delivery under the store lock.
func (m *Mock) recordDelivery(d *ActionGroupDelivery) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deliveries = append(m.deliveries, *d)
}

// alarmPayload renders the notification body delivered to a webhook receiver.
func alarmPayload(alarm *alarmData, newState string, now time.Time) string {
	return `{"alertName":"` + alarm.Name +
		`","namespace":"` + alarm.Namespace +
		`","metricName":"` + alarm.MetricName +
		`","newState":"` + newState +
		`","timestamp":"` + now.UTC().Format(time.RFC3339) + `"}`
}

// actionGroupEnabled reads properties.enabled, defaulting to true (real action
// groups are enabled unless explicitly disabled).
func actionGroupEnabled(props map[string]any) bool {
	enabled, ok := props["enabled"].(bool)
	if !ok {
		return true
	}

	return enabled
}

// parseActionGroupReceivers flattens the typed receiver arrays of an action
// group's properties into a uniform receiver list.
func parseActionGroupReceivers(props map[string]any) []ActionGroupReceiver {
	specs := []struct {
		key, typ, endpointKey string
	}{
		{"emailReceivers", receiverEmail, "emailAddress"},
		{"webhookReceivers", receiverWebhook, "serviceUri"},
		{"smsReceivers", receiverSMS, "phoneNumber"},
		{"azureFunctionReceivers", receiverAzureFunction, "httpTriggerUrl"},
	}

	out := make([]ActionGroupReceiver, 0, len(specs))
	for _, s := range specs {
		out = append(out, receiversOf(props, s.key, s.typ, s.endpointKey)...)
	}

	return out
}

// receiversOf extracts the receivers under properties[key], reading each entry's
// "name" and the endpoint under endpointKey.
func receiversOf(props map[string]any, key, typ, endpointKey string) []ActionGroupReceiver {
	raw, ok := props[key].([]any)
	if !ok {
		return nil
	}

	out := make([]ActionGroupReceiver, 0, len(raw))

	for _, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		out = append(out, ActionGroupReceiver{
			Type:     typ,
			Name:     mapString(item, "name"),
			Endpoint: mapString(item, endpointKey),
		})
	}

	return out
}

// mapString reads a string field from a decoded JSON object, empty when absent.
func mapString(m map[string]any, key string) string {
	s, _ := m[key].(string)

	return s
}
