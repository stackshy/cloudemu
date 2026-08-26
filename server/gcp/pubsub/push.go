package pubsub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// pushDeliveryTimeout bounds a single push POST so a slow or unresponsive
// endpoint can never stall a publish. It mirrors the Azure Monitor webhook /
// Event Grid deliverers.
const pushDeliveryTimeout = 10 * time.Second

// pushEnvelope is the body real Pub/Sub POSTs to a push subscription's endpoint:
// the message plus the subscription resource name it was delivered for.
type pushEnvelope struct {
	Message      pubsubMessage `json:"message"`
	Subscription string        `json:"subscription"`
}

// pushConfigJSON is the parsed subscription.pushConfig: only pushEndpoint is
// needed to know where to POST.
type pushConfigJSON struct {
	PushEndpoint string            `json:"pushEndpoint"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// pushSub is a snapshot of one push subscription: its short name (map key), its
// resource name (echoed in the envelope) and the endpoint to POST to.
type pushSub struct {
	name     string
	fullName string
	endpoint string
}

// publishedMessage pairs a just-stored message's topic-log index (so a
// successful push can auto-ack it for that subscription) with the wire message
// pushed / delivered to a function.
type publishedMessage struct {
	idx int
	msg pubsubMessage
}

// PushDeliverer POSTs a push envelope to a subscription's endpoint, reporting
// whether the endpoint accepted it (a 2xx). New() installs a real-HTTP default,
// so a push subscription delivers in production without any wiring;
// SetPushDeliverer is a test seam that swaps in a fake to assert delivery.
type PushDeliverer interface {
	Deliver(ctx context.Context, endpoint string, body []byte) bool
}

// FunctionInvoker delivers a published message to every Cloud Function whose
// eventTrigger targets the topic. The Cloud Functions handler implements it and
// is wired in on server construction; publish calls it best-effort so a slow or
// failing function never fails the publish.
type FunctionInvoker interface {
	InvokeForTopic(ctx context.Context, project, topic string, event []byte)
}

// httpPushDeliverer is the production PushDeliverer: a best-effort real HTTP
// POST with a bounded client. Transport errors and non-2xx responses report
// "not accepted" so the message stays available for redelivery / pull.
type httpPushDeliverer struct {
	client *http.Client
}

func newHTTPPushDeliverer() *httpPushDeliverer {
	return &httpPushDeliverer{client: &http.Client{Timeout: pushDeliveryTimeout}}
}

func (d *httpPushDeliverer) Deliver(ctx context.Context, endpoint string, body []byte) bool {
	reqCtx, cancel := context.WithTimeout(ctx, pushDeliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false
	}

	req.Header.Set("Content-Type", contentTypeJSON)

	resp, err := d.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}

// SetPushDeliverer overrides the push deliverer. Test seam: a test injects a
// fake to observe delivery without a live receiver. New() already installs a
// real-HTTP default, so production never calls this.
func (h *Handler) SetPushDeliverer(d PushDeliverer) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.pushDeliverer = d
}

// SetFunctionInvoker wires the Cloud Functions backend so a publish invokes any
// function whose eventTrigger targets the topic. Nil (the default) leaves
// function delivery a no-op.
func (h *Handler) SetFunctionInvoker(fi FunctionInvoker) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.functionInvoker = fi
}

// buildPubsubMessage renders a stored message as the wire message shape carried
// by both a push envelope and a Cloud Function event.
func buildPubsubMessage(id string, msg *storedMessage) pubsubMessage {
	return pubsubMessage{
		MessageID:   id,
		Data:        encodeData(msg.body),
		Attributes:  msg.attributes,
		OrderingKey: msg.orderingKey,
		PublishTime: msg.publishTime.Format(time.RFC3339Nano),
	}
}

// pushSubscribersLocked snapshots the push subscriptions on a topic: those with
// a pushConfig.pushEndpoint set that are not detached. The caller holds h.mu.
func (h *Handler) pushSubscribersLocked(topicShort string) []pushSub {
	var subs []pushSub

	for name, sub := range h.subs {
		if sub.topic != topicShort || sub.cfg.Detached {
			continue
		}

		endpoint := pushEndpoint(sub.cfg.PushConfig)
		if endpoint == "" {
			continue
		}

		subs = append(subs, pushSub{name: name, fullName: sub.cfg.Name, endpoint: endpoint})
	}

	return subs
}

// pushEndpoint extracts pushConfig.pushEndpoint from a subscription's raw
// pushConfig JSON, returning "" when the subscription has no push endpoint.
func pushEndpoint(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var pc pushConfigJSON
	if err := json.Unmarshal(raw, &pc); err != nil {
		return ""
	}

	return pc.PushEndpoint
}

// dispatchPublished performs the cross-service delivery a publish triggers,
// outside h.mu so slow HTTP / function calls never hold the lock:
//
//   - every Cloud Function whose eventTrigger targets the topic is invoked with
//     each message as its event (best-effort);
//   - every push subscription's endpoint is POSTed each message's push envelope;
//     a message a push endpoint accepts (2xx) is auto-acked for that
//     subscription so a fallback pull does not redeliver it, while an
//     unaccepted message is left for redelivery / pull.
func (h *Handler) dispatchPublished(
	ctx context.Context, project, topicShort string, msgs []publishedMessage, pushSubs []pushSub,
) {
	h.invokeFunctions(ctx, project, topicShort, msgs)
	h.deliverPush(ctx, pushSubs, msgs)
}

func (h *Handler) invokeFunctions(ctx context.Context, project, topicShort string, msgs []publishedMessage) {
	if h.functionInvoker == nil {
		return
	}

	for i := range msgs {
		event, err := json.Marshal(msgs[i].msg)
		if err != nil {
			continue
		}

		h.functionInvoker.InvokeForTopic(ctx, project, topicShort, event)
	}
}

func (h *Handler) deliverPush(ctx context.Context, pushSubs []pushSub, msgs []publishedMessage) {
	if len(pushSubs) == 0 || h.pushDeliverer == nil {
		return
	}

	type ack struct {
		sub string
		idx int
	}

	var acks []ack

	for _, ps := range pushSubs {
		for i := range msgs {
			body, err := json.Marshal(pushEnvelope{Message: msgs[i].msg, Subscription: ps.fullName})
			if err != nil {
				continue
			}

			if h.pushDeliverer.Deliver(ctx, ps.endpoint, body) {
				acks = append(acks, ack{sub: ps.name, idx: msgs[i].idx})
			}
		}
	}

	if len(acks) == 0 {
		return
	}

	h.mu.Lock()
	for _, a := range acks {
		if sub, ok := h.subs[a.sub]; ok {
			sub.acked[a.idx] = true
		}
	}
	h.mu.Unlock()
}
