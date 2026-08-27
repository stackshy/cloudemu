package compute

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// defaultMaxResults is GCP's default page size for list calls.
const defaultMaxResults = 500

// setLabels handles POST .../instances/{name}/setLabels: it replaces the
// instance's user labels (internal state tags are preserved).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setLabels(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var body setLabelsRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if !fingerprintMatches(body.LabelFingerprint, labelFingerprintFor(userLabels(inst.Tags))) {
		writeConditionNotMet(w, "labelFingerprint")
		return
	}

	// Remove existing user labels that the new set drops.
	var remove []string

	for k := range userLabels(inst.Tags) {
		if _, keep := body.Labels[k]; !keep {
			remove = append(remove, k)
		}
	}

	h.applyMutation(w, r, rp, inst.ID, "setLabels", body.Labels, remove, "")
}

// setMetadata handles POST .../instances/{name}/setMetadata.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setMetadata(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var body metadataBlock
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if !fingerprintMatches(body.Fingerprint, metadataResponse(decodeMetadata(inst.Tags)).Fingerprint) {
		writeConditionNotMet(w, "metadata fingerprint")
		return
	}

	h.applyMutation(w, r, rp, inst.ID, "setMetadata",
		map[string]string{keyMetadata: encodeJSON(body.Items)}, nil, "")
}

// setTags handles POST .../instances/{name}/setTags.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setTags(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var body tagsBlock
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if !fingerprintMatches(body.Fingerprint, fingerprint(strings.Join(decodeNetTags(inst.Tags), ","))) {
		writeConditionNotMet(w, "tags fingerprint")
		return
	}

	h.applyMutation(w, r, rp, inst.ID, "setTags",
		map[string]string{keyNetTags: encodeJSON(body.Items)}, nil, "")
}

// setMachineType handles POST .../instances/{name}/setMachineType.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setMachineType(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var body setMachineTypeRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Real GCP rejects changing the machine type of an instance that is not
	// TERMINATED (stopped) with a 400 — the VM must be stopped first.
	if gcpStatusFor(inst.State) != statusTerminated {
		gcprest.WriteError(w, http.StatusBadRequest, "conditionNotMet",
			"Instance "+rp.ResourceName+" must be stopped before the machine type can be changed.")

		return
	}

	h.applyMutation(w, r, rp, inst.ID, "setMachineType", nil, nil, machineTypeShort(body.MachineType))
}

// attachDisk handles POST .../instances/{name}/attachDisk.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) attachDisk(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var disk attachedDisk
	if !gcprest.DecodeJSON(w, r, &disk) {
		return
	}

	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	disks := append(decodeDisks(inst.Tags), disk)

	h.applyMutation(w, r, rp, inst.ID, "attachDisk",
		map[string]string{keyDisks: encodeJSON(disks)}, nil, "")
}

// detachDisk handles POST .../instances/{name}/detachDisk?deviceName=...
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) detachDisk(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	device := r.URL.Query().Get("deviceName")
	if device == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "deviceName is required")
		return
	}

	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	kept := make([]attachedDisk, 0)

	for _, d := range decodeDisks(inst.Tags) {
		if d.DeviceName != device {
			kept = append(kept, d)
		}
	}

	h.applyMutation(w, r, rp, inst.ID, "detachDisk",
		map[string]string{keyDisks: encodeJSON(kept)}, nil, "")
}

// applyMutation runs a GCP-specific instance mutation through the mutator
// capability and returns a DONE Operation.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) applyMutation(
	w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	instanceID, opType string, set map[string]string, remove []string, machineType string,
) {
	mutator, ok := h.compute.(instanceMutator)
	if !ok {
		writeNotImplemented(w, "instance mutation: "+opType)
		return
	}

	if err := mutator.MutateInstanceGCP(instanceID, set, remove, machineType); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", rp.ResourceName, opType)

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// fingerprintMatches enforces optimistic-concurrency on the update verbs: an
// incoming fingerprint must equal the resource's current one. An empty incoming
// fingerprint skips the check, matching real GCP's leniency when a client omits
// it.
func fingerprintMatches(incoming, current string) bool {
	return incoming == "" || incoming == current
}

// writeConditionNotMet responds with GCP's 412 conditionNotMet, returned when a
// stale fingerprint loses the optimistic-concurrency check on an update verb.
func writeConditionNotMet(w http.ResponseWriter, what string) {
	gcprest.WriteError(w, http.StatusPreconditionFailed, "conditionNotMet",
		what+" does not match; the resource was modified concurrently")
}

// parseMaxResults parses the maxResults query param, defaulting to GCP's page
// size when absent or invalid.
func parseMaxResults(raw string) int {
	if raw == "" {
		return defaultMaxResults
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultMaxResults
	}

	return n
}

// parseFilter compiles a GCP list filter into a predicate. It supports the
// common single-clause forms "<field> <op> <value>" where op is one of
// "=", "!=", "eq", "ne" and field is name/status/machineType/zone or a
// "labels.<key>" selector. An empty filter, an unparseable clause, or a
// clause naming a field the emulator does not model all match everything —
// mirroring real GCP's leniency (and gcprest.NameMatches) so an unknown
// field never silently excludes every instance.
func parseFilter(raw string) func(*instanceResponse) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return func(*instanceResponse) bool { return true }
	}

	field, op, value, ok := splitFilter(raw)
	if !ok {
		return func(*instanceResponse) bool { return true }
	}

	negate := op == "!=" || op == "ne"

	return func(resp *instanceResponse) bool {
		got, known := filterField(resp, field)
		if !known {
			return true
		}

		eq := got == value

		return eq != negate
	}
}

func splitFilter(raw string) (field, op, value string, ok bool) {
	for _, candidate := range []string{"!=", "=", " ne ", " eq "} {
		if idx := strings.Index(raw, candidate); idx >= 0 {
			field = strings.TrimSpace(raw[:idx])
			value = strings.Trim(strings.TrimSpace(raw[idx+len(candidate):]), `"'`)
			op = strings.TrimSpace(candidate)

			return field, op, value, field != ""
		}
	}

	return "", "", "", false
}

// filterField resolves a filter field to its value on resp. The second return
// reports whether the field is one the emulator models: an unknown field
// returns known=false so the caller can match-all rather than exclude all.
func filterField(resp *instanceResponse, field string) (value string, known bool) {
	switch field {
	case "name":
		return resp.Name, true
	case "status":
		return resp.Status, true
	case "machineType":
		return lastSegment(resp.MachineType), true
	case "zone":
		return lastSegment(resp.Zone), true
	}

	if key := strings.TrimPrefix(field, "labels."); key != field {
		return resp.Labels[key], true
	}

	return "", false
}
