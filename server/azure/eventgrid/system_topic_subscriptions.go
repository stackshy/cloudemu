package eventgrid

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveSystemTopicSubscription routes
// .../systemTopics/{t}/eventSubscriptions[/{name}].
func (h *Handler) serveSystemTopicSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listSystemTopicSubscriptions(w, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateSystemTopicSubscription(w, r, rp)
	case http.MethodGet:
		h.getSystemTopicSubscription(w, rp)
	case http.MethodDelete:
		h.deleteSystemTopicSubscription(w, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// systemTopicSubscriptionJSON builds the ARM EventSubscription element for a
// subscription stored under a system topic, stamping the read-only topic id and
// provisioning state onto the round-tripped properties.
func systemTopicSubscriptionJSON(rp *azurearm.ResourcePath, props json.RawMessage) eventSubscriptionJSON {
	id := systemTopicID(rp) + "/" + subEventSubscriptions + "/" + rp.SubResourceName

	return eventSubscriptionJSON{
		ID:         id,
		Name:       rp.SubResourceName,
		Type:       systemTopicSubscriptionType,
		Properties: enrichSubscriptionProperties(props, systemTopicID(rp)),
	}
}

func (h *Handler) createOrUpdateSystemTopicSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		Properties json.RawMessage `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.systemTopics[key]
	if rec == nil {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "system topic not found")

		return
	}

	rec.subscriptions[rp.SubResourceName] = body.Properties
	out := systemTopicSubscriptionJSON(rp, body.Properties)
	source := rec.source
	h.mu.Unlock()

	// Bridge the wire-created subscription to the delivery path: register it as a
	// rule on the system topic's isolated delivery bus so the source producer's
	// PutEvents matches and delivers it (mirrors the custom-topic path).
	busName := h.ensureSystemTopicBus(source)
	h.registerSystemTopicSubscription(busName, rp.SubResourceName, body.Properties)

	azurearm.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) getSystemTopicSubscription(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()

	rec := h.systemTopics[key]
	if rec == nil {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "system topic not found")

		return
	}

	props, found := rec.subscriptions[rp.SubResourceName]
	h.mu.RUnlock()

	if !found {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "event subscription not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, systemTopicSubscriptionJSON(rp, props))
}

func (h *Handler) deleteSystemTopicSubscription(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	var source string

	if rec := h.systemTopics[key]; rec != nil {
		source = rec.source
		delete(rec.subscriptions, rp.SubResourceName)
	}

	h.mu.Unlock()

	// Drop the delivery rule so the deleted subscription stops receiving events.
	h.unregisterSystemTopicSubscription(systemTopicBusName(source), rp.SubResourceName)

	// The SDK's BeginDelete LRO completes on a 200 first response.
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listSystemTopicSubscriptions(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()

	rec := h.systemTopics[key]
	if rec == nil {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "system topic not found")

		return
	}

	names := make([]string, 0, len(rec.subscriptions))
	for name := range rec.subscriptions {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]eventSubscriptionJSON, 0, len(names))

	for _, name := range names {
		props := rec.subscriptions[name]
		scoped := *rp
		scoped.SubResourceName = name
		out = append(out, systemTopicSubscriptionJSON(&scoped, props))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, eventSubscriptionListResult{Value: out})
}
