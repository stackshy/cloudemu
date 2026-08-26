package cloudfunctions

import (
	"context"
	"strings"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// InvokeForTopic delivers a published Pub/Sub message to every Cloud Function
// whose eventTrigger targets the topic: gen1 functions whose
// eventTrigger.resource is the topic, and gen2 functions whose
// eventTrigger.pubsubTopic is the topic. Each matching function is invoked with
// the message as its event (the legacy Pub/Sub {data, attributes, messageId,
// publishTime} shape). Best-effort — a missing or failing function is swallowed
// so a publish never fails. It implements the pubsub handler's FunctionInvoker.
func (h *Handler) InvokeForTopic(ctx context.Context, project, topic string, event []byte) {
	fullTopic := "projects/" + project + "/topics/" + topic

	h.mu.RLock()
	targets := make([]string, 0)

	for key, meta := range h.gen1Meta {
		if gen1PubsubTriggerMatches(meta.eventTrigger, topic, fullTopic) {
			targets = append(targets, lastSegment(key))
		}
	}

	for _, fn := range h.gen2 {
		if fn.EventTrigger != nil && topicRefMatches(fn.EventTrigger.PubsubTopic, topic, fullTopic) {
			targets = append(targets, lastSegment(fn.Name))
		}
	}
	h.mu.RUnlock()

	for _, name := range targets {
		_, _ = h.fn.Invoke(ctx, sdrv.InvokeInput{
			FunctionName: name,
			Payload:      event,
			InvokeType:   "Event",
		})
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
