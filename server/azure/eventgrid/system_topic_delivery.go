package eventgrid

import (
	"encoding/json"
	"strings"
)

// systemTopicDelivery is the optional capability an event bus backend exposes to
// isolate system-topic event delivery from user-facing custom topics. System
// topics are an Azure-only concept, so the Azure eventgrid.Mock implements this
// while other backends do not; the wire handler feature-detects it. Because the
// delivery buses live in a store the custom-topic CRUD paths never touch, a
// custom topic and a system delivery bus can share a name (e.g. the fixed
// storage-account name a Blob Storage system topic keys on) without either
// clobbering or leaking into the other.
type systemTopicDelivery interface {
	// EnsureSystemDeliveryBus makes sure an isolated delivery bus with the given
	// name exists (no-op if it already does).
	EnsureSystemDeliveryBus(name string)
	// PutSystemDeliveryRule registers/updates a subscription rule (raw ARM
	// EventSubscription properties: destination + filter) on a delivery bus.
	PutSystemDeliveryRule(busName, ruleName, properties string) error
	// DeleteSystemDeliveryRule removes a subscription rule from a delivery bus.
	DeleteSystemDeliveryRule(busName, ruleName string) error
}

// systemTopicBusName derives the delivery bus that backs a system topic from its
// source: the trailing segment of the source resource id (the storage account
// name for a Microsoft.Storage system topic), which is exactly the key the
// source producer stamps on event.EventBus — the Blob Storage producer publishes
// with EventBus = the account name. Empty when source is empty.
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

// ensureSystemTopicBus provisions the isolated delivery bus for a system topic
// source and returns its name. Best-effort: with no delivery-capable backend, or
// an empty source, it is a no-op (returning the derived name, possibly "").
func (h *Handler) ensureSystemTopicBus(source string) string {
	bus := systemTopicBusName(source)
	if bus == "" || h.sysDelivery == nil {
		return bus
	}

	h.sysDelivery.EnsureSystemDeliveryBus(bus)

	return bus
}

// registerSystemTopicSubscription mirrors the custom-topic event-subscription
// path for a system topic: it registers the subscription as a delivery rule on
// the system topic's isolated bus so the source producer's PutEvents matches its
// filter and delivers to its destination. Best-effort.
func (h *Handler) registerSystemTopicSubscription(busName, name string, props json.RawMessage) {
	if busName == "" || h.sysDelivery == nil {
		return
	}

	_ = h.sysDelivery.PutSystemDeliveryRule(busName, name, string(props))
}

// unregisterSystemTopicSubscription removes the delivery rule a system-topic
// subscription registered, so a deleted subscription (or a deleted system topic)
// stops receiving events. Best-effort.
func (h *Handler) unregisterSystemTopicSubscription(busName, name string) {
	if busName == "" || h.sysDelivery == nil {
		return
	}

	_ = h.sysDelivery.DeleteSystemDeliveryRule(busName, name)
}
