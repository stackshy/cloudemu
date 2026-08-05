package eventarc

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func (h *Handler) createTrigger(w http.ResponseWriter, r *http.Request, rt *route) {
	triggerID := r.URL.Query().Get("triggerId")
	if triggerID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "triggerId is required")
		return
	}

	var body triggerJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	bus := channelName(rt.location)

	// Auto-provision the location's backing event bus on first use. Ignore an
	// already-exists error so concurrent/repeat creates are idempotent.
	if err := h.ensureChannel(r, rt, bus); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if _, err := h.bus.GetRule(r.Context(), bus, triggerID); err == nil {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.AlreadyExists, "trigger %q already exists", triggerID))
		return
	}

	if _, err := h.bus.PutRule(r.Context(), &ebdriver.RuleConfig{
		Name:         triggerID,
		EventBus:     bus,
		EventPattern: encodeEventPattern(body.EventFilters),
		Description:  encodeTriggerMeta(body.ServiceAccount, body.Labels),
	}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if target, ok := destinationTarget(body.Destination); ok {
		if perr := h.bus.PutTargets(r.Context(), bus, triggerID, []ebdriver.Target{target}); perr != nil {
			// Roll back the rule created above so a failed Create doesn't leave
			// an orphaned trigger that blocks a later Create with the same id.
			_ = h.bus.DeleteRule(r.Context(), bus, triggerID)
			gcprest.WriteCErr(w, perr)
			return
		}
	}

	// Re-fetch so the response carries the stored targets/destination.
	stored, err := h.bus.GetRule(r.Context(), bus, triggerID)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, triggerID,
		typedResponse(triggerTypeURL, toTriggerJSON(rt.project, rt.location, stored))))
}

// triggerTypeURL is the protobuf Any type URL a GAPIC eventarc client expects
// in a done LRO's response so CreateTriggerOperation.Wait() can decode it.
const triggerTypeURL = "type.googleapis.com/google.cloud.eventarc.v1.Trigger"

// typedResponse renders v as a google.protobuf.Any JSON object (resource fields
// + "@type"); a GAPIC .Wait() can't unmarshal the response without @type.
func typedResponse(typeURL string, v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}

	m["@type"] = typeURL

	return m
}

func (h *Handler) getTrigger(w http.ResponseWriter, r *http.Request, rt *route) {
	rule, err := h.bus.GetRule(r.Context(), channelName(rt.location), rt.trigger)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toTriggerJSON(rt.project, rt.location, rule))
}

func (h *Handler) listTriggers(w http.ResponseWriter, r *http.Request, rt *route) {
	bus := channelName(rt.location)

	rules, err := h.bus.ListRules(r.Context(), bus)
	if err != nil {
		// A location with no triggers has no backing bus yet; report an empty
		// list rather than an error, matching Eventarc's List semantics.
		if cerrors.IsNotFound(err) {
			gcprest.WriteJSON(w, http.StatusOK, listTriggersResponse{Triggers: []triggerJSON{}})
			return
		}

		gcprest.WriteCErr(w, err)

		return
	}

	out := make([]triggerJSON, 0, len(rules))
	for i := range rules {
		out = append(out, toTriggerJSON(rt.project, rt.location, &rules[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listTriggersResponse{Triggers: out})
}

func (h *Handler) deleteTrigger(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.bus.DeleteRule(r.Context(), channelName(rt.location), rt.trigger); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, doneOperation(rt, rt.trigger, nil))
}

// ensureChannel creates the location's backing event bus if it does not already
// exist, recording the request's project as its scope. An already-exists result
// is treated as success.
func (h *Handler) ensureChannel(r *http.Request, rt *route, bus string) error {
	if _, err := h.bus.GetEventBus(r.Context(), bus); err == nil {
		return nil
	}

	_, err := h.bus.CreateEventBus(r.Context(), ebdriver.EventBusConfig{
		Name:  bus,
		Scope: scope.Scope{Project: rt.project},
	})
	if err != nil && !cerrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

// doneOperation builds a completed long-running operation envelope.
func doneOperation(rt *route, id string, response any) operationJSON {
	return operationJSON{
		Name:     "projects/" + rt.project + "/locations/" + rt.location + "/operations/op-" + id,
		Done:     true,
		Response: response,
	}
}
