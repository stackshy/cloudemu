package eventarc

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
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

	target, ok := h.validateTriggerBody(w, r, &body)
	if !ok {
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
		Description: encodeTriggerMeta(triggerMeta{
			ServiceAccount: body.ServiceAccount,
			Labels:         body.Labels,
			UID:            idgen.GenerateID("trigger-"),
		}),
	}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if perr := h.bus.PutTargets(r.Context(), bus, triggerID, []ebdriver.Target{target}); perr != nil {
		// Roll back the rule created above so a failed Create doesn't leave
		// an orphaned trigger that blocks a later Create with the same id.
		_ = h.bus.DeleteRule(r.Context(), bus, triggerID)

		gcprest.WriteCErr(w, perr)

		return
	}

	// Re-fetch so the response carries the stored targets/destination.
	stored, err := h.bus.GetRule(r.Context(), bus, triggerID)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(rt, triggerID,
		typedResponse(triggerTypeURL, toTriggerJSON(rt.project, rt.location, stored))))
}

// validateTriggerBody validates a Create request's eventFilters and
// destination, writing the appropriate error response and returning ok=false
// when either check fails. On success it returns the driver Target the
// validated destination folds into — a trigger must route somewhere, and real
// Eventarc rejects a trigger with no destination (or one naming a resource
// that doesn't exist) with INVALID_ARGUMENT / NOT_FOUND rather than storing a
// dead route.
func (h *Handler) validateTriggerBody(w http.ResponseWriter, r *http.Request, body *triggerJSON) (ebdriver.Target, bool) {
	if msg := validateEventFilters(body.EventFilters); msg != "" {
		gcprest.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", msg)
		return ebdriver.Target{}, false
	}

	target, ok := destinationTarget(body.Destination)
	if !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "destination is required")
		return ebdriver.Target{}, false
	}

	if err := h.validateDestination(r.Context(), body.Destination); err != nil {
		gcprest.WriteCErr(w, err)
		return ebdriver.Target{}, false
	}

	return target, true
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

	page, perr := pagination.PaginateSorted(rules,
		func(a, b ebdriver.Rule) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), triggerPageSize(r))
	if perr != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	out := make([]triggerJSON, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, toTriggerJSON(rt.project, rt.location, &page.Items[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listTriggersResponse{
		Triggers:      out,
		NextPageToken: page.NextPageToken,
	})
}

const (
	defaultTriggerPageSize = 50
	maxTriggerPageSize     = 1000
)

// triggerPageSize reads ?pageSize, clamping to a sane default and ceiling.
func triggerPageSize(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil || n <= 0 {
		return defaultTriggerPageSize
	}

	if n > maxTriggerPageSize {
		return maxTriggerPageSize
	}

	return n
}

func (h *Handler) deleteTrigger(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.bus.DeleteRule(r.Context(), channelName(rt.location), rt.trigger); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(rt, rt.trigger, nil))
}

// patchTrigger applies an updateTrigger call. Only the fields named in
// ?updateMask are changed (an empty mask updates every field present in the
// body); uid and createTime are preserved. Real Eventarc supports patching the
// destination, event filters, service account, and labels.
func (h *Handler) patchTrigger(w http.ResponseWriter, r *http.Request, rt *route) {
	bus := channelName(rt.location)

	existing, err := h.bus.GetRule(r.Context(), bus, rt.trigger)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var body triggerJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	mask := parseUpdateMask(r.URL.Query().Get("updateMask"))
	cur := toTriggerJSON(rt.project, rt.location, existing)
	meta := decodeTriggerMeta(existing.Description)

	if !h.applyMaskedFields(w, r, mask, &body, &cur, &meta) {
		return
	}

	meta.Revision++

	if perr := h.applyTriggerUpdate(r, rt, &cur, meta); perr != nil {
		gcprest.WriteCErr(w, perr)
		return
	}

	stored, gerr := h.bus.GetRule(r.Context(), bus, rt.trigger)
	if gerr != nil {
		gcprest.WriteCErr(w, gerr)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(rt, rt.trigger,
		typedResponse(triggerTypeURL, toTriggerJSON(rt.project, rt.location, stored))))
}

// applyMaskedFields validates and applies onto cur/meta the fields a Patch
// request's updateMask names, writing the appropriate error response and
// returning ok=false when a validated field (eventFilters or destination)
// fails validation. Unvalidated fields (serviceAccount, labels) are applied
// unconditionally.
func (h *Handler) applyMaskedFields(
	w http.ResponseWriter, r *http.Request, mask updateMask, body, cur *triggerJSON, meta *triggerMeta,
) bool {
	if mask.has("eventFilters") {
		if msg := validateEventFilters(body.EventFilters); msg != "" {
			gcprest.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", msg)
			return false
		}

		cur.EventFilters = body.EventFilters
	}

	if mask.has("serviceAccount") {
		meta.ServiceAccount = body.ServiceAccount
	}

	if mask.has("labels") {
		meta.Labels = body.Labels
	}

	if mask.has("destination") {
		if _, ok := destinationTarget(body.Destination); !ok {
			gcprest.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "destination is required")
			return false
		}

		if err := h.validateDestination(r.Context(), body.Destination); err != nil {
			gcprest.WriteCErr(w, err)
			return false
		}

		cur.Destination = body.Destination
	}

	return true
}

// applyTriggerUpdate persists the mutated trigger back onto the driver rule and
// its targets.
func (h *Handler) applyTriggerUpdate(r *http.Request, rt *route, cur *triggerJSON, meta triggerMeta) error {
	bus := channelName(rt.location)

	if _, err := h.bus.PutRule(r.Context(), &ebdriver.RuleConfig{
		Name:         rt.trigger,
		EventBus:     bus,
		EventPattern: encodeEventPattern(cur.EventFilters),
		Description:  encodeTriggerMeta(meta),
	}); err != nil {
		return err
	}

	target, _ := destinationTarget(cur.Destination)

	return h.bus.PutTargets(r.Context(), bus, rt.trigger, []ebdriver.Target{target})
}

// updateMask is a parsed FieldMask: an empty mask means "update every field
// present in the body".
type updateMask struct {
	fields map[string]bool
	all    bool
}

func (m updateMask) has(field string) bool {
	return m.all || m.fields[field]
}

func parseUpdateMask(raw string) updateMask {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return updateMask{all: true}
	}

	fields := map[string]bool{}

	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields[f] = true
		}
	}

	return updateMask{fields: fields}
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

// doneOperation builds a completed long-running operation envelope and records
// it with the shared LRO poller so a client polling the returned name resolves
// the same done operation (with its typed response) in the full server.
func (h *Handler) doneOperation(rt *route, id string, response any) operationJSON {
	name := "projects/" + rt.project + "/locations/" + rt.location + "/operations/op-" + id
	h.ops.Register(name, response)

	return operationJSON{
		Name:     name,
		Done:     true,
		Response: response,
	}
}
