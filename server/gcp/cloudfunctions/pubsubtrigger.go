package cloudfunctions

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// pubsubCloudEventType is the CloudEvent type Eventarc stamps on a gen2 Cloud
// Function's Pub/Sub trigger delivery — the same value real GCP requires on
// EventTrigger.EventType for a Pub/Sub-triggered gen2 function.
const pubsubCloudEventType = "google.cloud.pubsub.topic.v1.messagePublished"

// pubsubMessageEvent mirrors the JSON shape the pubsub handler marshals as
// InvokeForTopic's event parameter (the flat pubsub.pubsubMessage: data,
// attributes, messageId, publishTime, orderingKey). It is redeclared here
// (server/gcp/pubsub does not export a message type) so InvokeForTopic can
// decode the fields it needs to rebuild the gen2 CloudEvent envelope below.
type pubsubMessageEvent struct {
	Data        string            `json:"data"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	OrderingKey string            `json:"orderingKey,omitempty"`
	MessageID   string            `json:"messageId,omitempty"`
	PublishTime string            `json:"publishTime,omitempty"`
}

// gen2PubsubEvent is the structured-mode CloudEvent body a real gen2
// Eventarc-backed Pub/Sub trigger receives: the CloudEvents envelope
// (specversion/type/source/id/time) wrapping the Pub/Sub MessagePublishedData
// payload ({message, subscription}). Real Eventarc delivers binary-mode
// CloudEvents (ce-* as HTTP headers, the bare data as the request body), but
// driver.InvokeInput carries a payload only — no headers — so structured mode,
// the whole envelope as one JSON body, is the closest self-contained
// equivalent the emulator's invoke contract can deliver.
type gen2PubsubEvent struct {
	SpecVersion     string              `json:"specversion"`
	Type            string              `json:"type"`
	Source          string              `json:"source"`
	ID              string              `json:"id"`
	Time            string              `json:"time,omitempty"`
	DataContentType string              `json:"datacontenttype"`
	Data            gen2PubsubEventData `json:"data"`
}

// gen2PubsubEventData is the Pub/Sub MessagePublishedData shape: the message
// plus the (Eventarc-managed) subscription it was delivered through.
type gen2PubsubEventData struct {
	Message      pubsubMessageEvent `json:"message"`
	Subscription string             `json:"subscription"`
}

// InvokeForTopic delivers a published Pub/Sub message to every Cloud Function
// whose eventTrigger targets the topic: gen1 functions whose
// eventTrigger.resource is the topic get the message as their event (the
// legacy Pub/Sub {data, attributes, messageId, publishTime} shape); gen2
// functions whose eventTrigger.pubsubTopic is the topic get the CloudEvent
// envelope a real Eventarc-backed trigger delivers (see gen2PubsubEvent).
// Best-effort — a missing or failing function is swallowed so a publish never
// fails. It implements the pubsub handler's FunctionInvoker.
//
// ctx carries the re-entrant delivery depth (internal/recursionguard): a
// function invoked from here that republishes to its own trigger topic would
// otherwise recurse synchronously and unbounded (Publish -> InvokeForTopic ->
// Invoke -> handler -> Publish -> ...), so once the depth reaches
// recursionguard.MaxDepth this whole delivery hop is dropped.
func (h *Handler) InvokeForTopic(ctx context.Context, project, topic string, event []byte) {
	depth := recursionguard.Depth(ctx)
	if depth >= recursionguard.MaxDepth {
		return
	}

	ctx = recursionguard.WithDepth(ctx, depth+1)

	gen1Targets, gen2Targets := h.pubsubTargetsLocked(project, topic)

	for _, name := range gen1Targets {
		_, _ = h.fn.Invoke(ctx, sdrv.InvokeInput{FunctionName: name, Payload: event, InvokeType: "Event"})
	}

	h.invokeGen2Targets(ctx, project, topic, gen2Targets, event)
}

// pubsubTargetsLocked snapshots the gen1 and gen2 function (short) names whose
// eventTrigger targets topic.
func (h *Handler) pubsubTargetsLocked(project, topic string) (gen1, gen2 []string) {
	fullTopic := "projects/" + project + "/topics/" + topic

	h.mu.RLock()
	defer h.mu.RUnlock()

	for key, meta := range h.gen1Meta {
		if gen1PubsubTriggerMatches(meta.eventTrigger, topic, fullTopic) {
			gen1 = append(gen1, lastSegment(key))
		}
	}

	for _, fn := range h.gen2 {
		if fn.EventTrigger != nil && topicRefMatches(fn.EventTrigger.PubsubTopic, topic, fullTopic) {
			gen2 = append(gen2, lastSegment(fn.Name))
		}
	}

	return gen1, gen2
}

// invokeGen2Targets decodes event into the flat message fields and invokes
// each gen2 target with the CloudEvent-wrapped payload a real Eventarc
// delivery carries. A malformed event (never produced by the pubsub handler,
// but defensive) drops gen2 delivery only; the gen1 delivery above already
// completed unaffected.
func (h *Handler) invokeGen2Targets(ctx context.Context, project, topic string, targets []string, event []byte) {
	if len(targets) == 0 {
		return
	}

	var msg pubsubMessageEvent
	if err := json.Unmarshal(event, &msg); err != nil {
		return
	}

	for _, name := range targets {
		payload, err := json.Marshal(buildGen2PubsubEvent(project, topic, name, msg))
		if err != nil {
			continue
		}

		_, _ = h.fn.Invoke(ctx, sdrv.InvokeInput{FunctionName: name, Payload: payload, InvokeType: "Event"})
	}
}

// buildGen2PubsubEvent wraps a published message into the CloudEvent shape a
// gen2 function's Pub/Sub eventTrigger delivers. subscription is a synthetic
// Eventarc-managed subscription name: real GCP auto-creates one per trigger
// (eventarc-<region>-<trigger>-sub-<suffix>), and CloudEmu does not model
// Eventarc trigger resources, so a stable name is derived from the target
// function so repeated invocations for the same function are consistent.
func buildGen2PubsubEvent(project, topic, function string, msg pubsubMessageEvent) gen2PubsubEvent {
	return gen2PubsubEvent{
		SpecVersion:     "1.0",
		Type:            pubsubCloudEventType,
		Source:          "//pubsub.googleapis.com/projects/" + project + "/topics/" + topic,
		ID:              msg.MessageID,
		Time:            msg.PublishTime,
		DataContentType: "application/json",
		Data: gen2PubsubEventData{
			Message:      msg,
			Subscription: "projects/" + project + "/subscriptions/eventarc-" + function + "-sub-000",
		},
	}
}

// gen1PubsubTriggerMatches reports whether a gen1 eventTrigger is a Pub/Sub
// trigger on the given topic. The "/topics/" guard keeps a same-named GCS
// bucket or Firestore document trigger from matching a topic.
func gen1PubsubTriggerMatches(et *eventTrigger, topic, fullTopic string) bool {
	if et == nil || !strings.Contains(et.Resource, "/topics/") {
		return false
	}

	return topicRefMatches(et.Resource, topic, fullTopic)
}

// topicRefMatches reports whether a topic reference (a full resource name or, as
// a tolerance, a bare topic id) points at the topic.
func topicRefMatches(ref, topic, fullTopic string) bool {
	if ref == "" {
		return false
	}

	return ref == fullTopic || lastSegment(ref) == topic
}
