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
		Name:                rp.ResourceName,
		Tags:                tagsFromPtr(body.Tags),
		Scope:               scope.Scope{Subscription: rp.Subscription, ResourceGroup: rp.ResourceGroup},
		Region:              body.Location,
		InputSchema:         inputSchemaFromBody(&body),
		PublicNetworkAccess: publicNetworkAccessFromBody(&body),
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

// inputSchemaFromBody reads the caller's requested InputSchema off a Topics
// CreateOrUpdate request body. CreateEventBus falls back to the real default
// (EventGridSchema) when this is empty; UpdateEventBus never sets it, so an
// update request's value is intentionally ignored — Event Grid does not allow
// changing a topic's input schema after creation.
func inputSchemaFromBody(body *topicJSON) string {
	if body.Properties == nil {
		return ""
	}

	return body.Properties.InputSchema
}

// publicNetworkAccessFromBody reads the caller's requested PublicNetworkAccess
// off a Topics CreateOrUpdate request body. CreateEventBus falls back to the
// real default (Enabled) when this is empty.
func publicNetworkAccessFromBody(body *topicJSON) string {
	if body.Properties == nil {
		return ""
	}

	return body.Properties.PublicNetworkAccess
}

// topicUpdateJSON is the Topics.Update (PATCH) request body: mutable tags plus
// mutable properties (publicNetworkAccess). Identity and input schema are not
// changeable here, mirroring real Event Grid.
type topicUpdateJSON struct {
	Tags       map[string]*string `json:"tags,omitempty"`
	Properties *struct {
		PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
	} `json:"properties,omitempty"`
}

// updateTopic maps Topics.Update (PATCH) onto UpdateEventBus: it merges the
// supplied tags onto the topic's existing tags and applies the mutable
// publicNetworkAccess, returning the updated topic (200).
func (h *Handler) updateTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body topicUpdateJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	current, err := h.bus.GetEventBus(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	cfg := ebdriver.EventBusConfig{
		Name:                rp.ResourceName,
		Tags:                mergeTags(current.Tags, tagsFromPtr(body.Tags)),
		PublicNetworkAccess: updatePublicNetworkAccess(&body),
	}

	info, uerr := h.bus.UpdateEventBus(r.Context(), cfg)
	if uerr != nil {
		azurearm.WriteCErr(w, uerr)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toTopicJSON(rp, info))
}

// updatePublicNetworkAccess pulls the mutable publicNetworkAccess off a PATCH
// body, empty when the caller omitted properties (UpdateEventBus then leaves it
// unchanged).
func updatePublicNetworkAccess(body *topicUpdateJSON) string {
	if body.Properties == nil {
		return ""
	}

	return body.Properties.PublicNetworkAccess
}

// mergeTags overlays patch onto base, returning the merged set. A nil result
// (both empty) leaves UpdateEventBus's existing tags untouched.
func mergeTags(base, patch map[string]string) map[string]string {
	if len(base) == 0 && len(patch) == 0 {
		return nil
	}

	out := make(map[string]string, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}

	for k, v := range patch {
		out[k] = v
	}

	return out
}

func (h *Handler) getTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	info, err := h.bus.GetEventBus(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// An internal system-topic delivery bus is not a custom topic; hide it from
	// the Microsoft.EventGrid/topics surface even if its name collides.
	if isSystemTopicBus(info) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "topic not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toTopicJSON(rp, info))
}

// deleteTopic removes the topic. Topics.Delete is an LRO in the SDK whose
// polling accepts 202/204; returning 204 with no body completes the poller on
// the first response.
func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// Never let a custom-topic delete destroy an internal system-topic delivery
	// bus that happens to share the name; report the idempotent 204 instead.
	if info, err := h.bus.GetEventBus(r.Context(), rp.ResourceName); err == nil && isSystemTopicBus(info) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

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
