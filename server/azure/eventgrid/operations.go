package eventgrid

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createOrUpdateTopic maps Topics.CreateOrUpdate onto the eventbus driver:
// create when absent, otherwise apply the request's mutable fields (tags) via
// UpdateEventBus — ARM PUT semantics, so the caller's changes are never
// silently discarded.
func (h *Handler) createOrUpdateTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body topicJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := ebdriver.EventBusConfig{
		Name:  rp.ResourceName,
		Tags:  tagsFromPtr(body.Tags),
		Scope: scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
	}

	if _, err := h.bus.GetEventBus(r.Context(), rp.ResourceName); err == nil {
		info, uerr := h.bus.UpdateEventBus(r.Context(), cfg)
		if uerr != nil {
			azurearm.WriteCErr(w, uerr)
			return
		}
		// The armeventgrid SDK accepts only 201 for Topics.CreateOrUpdate,
		// so the update path answers 201 as well.
		azurearm.WriteJSON(w, http.StatusCreated, toTopicJSON(rp, info))
		return
	}

	info, err := h.bus.CreateEventBus(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// 201 Created with a terminal provisioningState completes the SDK's LRO
	// poller on the first response.
	azurearm.WriteJSON(w, http.StatusCreated, toTopicJSON(rp, info))
}

func (h *Handler) getTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.bus.GetEventBus(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toTopicJSON(rp, info))
}

// deleteTopic removes the topic. Topics.Delete is an LRO in the SDK whose
// polling accepts 202/204; returning 204 with no body completes the poller on
// the first response.
func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.bus.DeleteEventBus(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	infos, err := h.bus.ListEventBuses(r.Context(),
		scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]topicJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toTopicJSON(rp, &infos[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, topicListResult{Value: out})
}
