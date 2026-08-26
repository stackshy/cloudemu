package eventgrid

import (
	"context"
	"encoding/json"
	"strings"

	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// systemTopicBusMarker scopes the internal event bus that backs a system
// topic's delivery. A system topic is not a Microsoft.EventGrid/topics
// resource, yet its subscriptions must register as eventbus rules so the source
// producer's PutEvents — which matches on event.EventBus — delivers to them.
// The bus is therefore named for the source's leaf (the key the producer stamps
// on event.EventBus), so it can't also carry the user's real subscription scope
// without leaking into custom-topic list results. This sentinel subscription
// keeps it out of every real-scope Topics list while leaving the name free to
// match the producer.
const systemTopicBusMarker = "cloudemu-system-topic"

// systemTopicBusName derives the event bus that backs a system topic's delivery
// from its source: the trailing segment of the source resource id (the storage
// account name for a Microsoft.Storage system topic), which is exactly the key
// the source producer stamps on event.EventBus (see the Blob Storage producer,
// which publishes with EventBus = the account name). Empty when source is empty.
func systemTopicBusName(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if trimmed == "" {
		return ""
	}

	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}

	return trimmed
}

// systemTopicBusScope returns the sentinel scope every system-topic delivery bus
// is created under, hiding it from real-scope custom-topic lists.
func systemTopicBusScope() scope.Scope {
	return scope.Scope{Subscription: systemTopicBusMarker}
}

// isSystemTopicBus reports whether an event bus is an internal system-topic
// delivery bus (created under the sentinel scope) rather than a real custom
// topic, so the custom-topic read paths can treat it as absent.
func isSystemTopicBus(info *ebdriver.EventBusInfo) bool {
	return info != nil && info.Scope.Subscription == systemTopicBusMarker
}

// ensureSystemTopicBus makes sure the internal delivery bus for a system topic
// source exists (creating it under the sentinel scope on first use) and returns
// its name. Best-effort, mirroring the producer's decoupled eventing: a create
// failure just means no delivery, never a failed ARM request. Returns "" when
// source carries no name to key on.
func (h *Handler) ensureSystemTopicBus(ctx context.Context, source string) string {
	bus := systemTopicBusName(source)
	if bus == "" {
		return ""
	}

	if _, err := h.bus.GetEventBus(ctx, bus); err == nil {
		return bus
	}

	_, _ = h.bus.CreateEventBus(ctx, ebdriver.EventBusConfig{Name: bus, Scope: systemTopicBusScope()})

	return bus
}

// registerSystemTopicSubscription mirrors the custom-topic event-subscription
// path (createOrUpdateEventSubscription) for a system topic: it registers the
// subscription as an eventbus rule keyed by the system topic's delivery bus,
// carrying the raw ARM properties (destination + filter) verbatim, so the source
// producer's PutEvents matches its filter and delivers to its destination.
// Best-effort.
func (h *Handler) registerSystemTopicSubscription(ctx context.Context, busName, name string, props json.RawMessage) {
	if busName == "" {
		return
	}

	_, _ = h.bus.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        name,
		EventBus:    busName,
		Description: string(props),
		State:       "ENABLED",
	})
}

// unregisterSystemTopicSubscription removes the delivery rule a system-topic
// subscription registered, so a deleted subscription (or a deleted system topic)
// stops receiving events. Best-effort.
func (h *Handler) unregisterSystemTopicSubscription(ctx context.Context, busName, name string) {
	if busName == "" {
		return
	}

	_ = h.bus.DeleteRule(ctx, busName, name)
}
