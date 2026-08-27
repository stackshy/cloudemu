package gcp

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/pubsub"
)

// monitoringPubSubAdapter adapts the wire Pub/Sub handler to the monitoring
// mock's PubSubPublisher: an alert-policy breach that targets a pubsub
// notification channel publishes the incident to the channel's topic, fanning
// out to the topic's subscriptions exactly as an API publish would.
type monitoringPubSubAdapter struct {
	h *pubsub.Handler
}

// PublishIncident publishes data to the channel's topic. topic is the channel's
// stored topic reference (a projects/{p}/topics/{t} resource name, or a bare
// topic id); a bare id publishes under an empty project, which still reaches the
// topic's pull subscriptions (keyed by the short name).
func (a monitoringPubSubAdapter) PublishIncident(ctx context.Context, topic string, data []byte) {
	project, short := parseTopicRef(topic)
	a.h.PublishMessage(ctx, project, short, data, nil)
}

// parseTopicRef splits a projects/{p}/topics/{t} reference into its project and
// short topic name, tolerating a bare topic id (project empty).
func parseTopicRef(ref string) (project, topic string) {
	parts := strings.Split(ref, "/")
	if len(parts) == 4 && parts[0] == "projects" && parts[2] == "topics" {
		return parts[1], parts[3]
	}

	return "", ref
}
