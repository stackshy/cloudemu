package memorystore

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// createInstance handles POST .../instances?instanceId={i} — Create. The
// operation completes inline, so a done=true Operation carrying the new
// Instance is returned.
func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request, rt route) {
	instanceID := r.URL.Query().Get("instanceId")
	if instanceID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instanceId query parameter is required")
		return
	}

	var body instanceJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	info, err := h.cache.CreateCache(r.Context(), cachedriver.CacheConfig{
		Name:     instanceID,
		Engine:   "redis",
		NodeType: body.Tier,
		Tags:     instanceTags(&body, nil, nil),
		Scope:    scope.Scope{Project: rt.project},
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	inst := toInstanceJSON(rt.project, rt.location, instanceID, info)

	raw, mErr := json.Marshal(inst)
	if mErr != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
		return
	}

	op := h.doneOperation(rt.project, rt.location, "create-"+instanceID, raw)

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// getInstance handles GET .../instances/{i} — Get.
func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, rt route) {
	info, err := h.cache.GetCache(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toInstanceJSON(rt.project, rt.location, rt.name, info))
}

// listInstances handles GET .../instances — List, scoped to the request's
// project. It honors the pageSize/pageToken/filter query parameters and
// advertises a nextPageToken when the result set is truncated.
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, rt route) {
	infos, err := h.cache.ListCaches(r.Context(), scope.Scope{Project: rt.project})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	q := r.URL.Query()
	filter := q.Get("filter")

	out := make([]instanceJSON, 0, len(infos))
	for i := range infos {
		// CacheInfo.Name carries the driver's own resource id; the short
		// instance id (the map key) is recovered from its trailing segment.
		id := shortInstanceID(infos[i].Name)
		inst := toInstanceJSON(rt.project, rt.location, id, &infos[i])

		if matchesFilter(&inst, filter) {
			out = append(out, inst)
		}
	}

	page, perr := pagination.PaginateSorted(out,
		func(a, b instanceJSON) bool { return a.Name < b.Name },
		q.Get("pageToken"), atoiOr(q.Get("pageSize"), 0))
	if perr != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, listInstancesResponse{
		Instances:     page.Items,
		NextPageToken: page.NextPageToken,
	})
}

// atoiOr parses s as a non-negative int, returning def when s is empty or
// invalid.
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}

	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}

	return n
}

// matchesFilter applies a single AIP-160 equality term (e.g. "labels.env=prod")
// to an instance. An empty or unsupported filter matches everything, so results
// are never silently hidden by a filter cloudemu doesn't model.
func matchesFilter(inst *instanceJSON, filter string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}

	field, want, ok := parseEqualityFilter(filter)
	if !ok {
		return true
	}

	got, known := instanceFieldValue(inst, field)
	if !known {
		return true
	}

	return got == want
}

// parseEqualityFilter splits "field = value" into its trimmed, unquoted parts.
func parseEqualityFilter(filter string) (field, value string, ok bool) {
	i := strings.Index(filter, "=")
	if i < 0 {
		return "", "", false
	}

	field = strings.TrimSpace(filter[:i])
	value = strings.Trim(strings.TrimSpace(filter[i+1:]), `"`)

	if field == "" {
		return "", "", false
	}

	return field, value, true
}

// instanceFieldValue resolves a filter field path against an instance, reporting
// whether the field is one cloudemu can filter on.
func instanceFieldValue(inst *instanceJSON, field string) (value string, known bool) {
	switch field {
	case "name":
		return inst.Name, true
	case "displayName":
		return inst.DisplayName, true
	case "tier":
		return inst.Tier, true
	case "state":
		return inst.State, true
	}

	if k, ok := strings.CutPrefix(field, "labels."); ok {
		return inst.Labels[k], true
	}

	if k, ok := strings.CutPrefix(field, "redisConfigs."); ok {
		return inst.RedisConfigs[k], true
	}

	return "", false
}

// patchInstance handles PATCH .../instances/{i} — Update. Real clients change
// memorySizeGb, displayName, labels, redisConfigs, and replicaCount here, scoped
// by the updateMask: a field outside the mask is left untouched.
func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, rt route) {
	existing, err := h.cache.GetCache(r.Context(), rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	var body instanceJSON
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	mask := parseFieldMask(r.URL.Query().Get("updateMask"))

	nodeType := existing.NodeType
	if mask.has("tier") && body.Tier != "" {
		nodeType = body.Tier
	}

	updated, err := h.cache.UpdateCache(r.Context(), cachedriver.CacheConfig{
		Name:     rt.name,
		Engine:   "redis",
		NodeType: nodeType,
		Tags:     instanceTags(&body, existing.Tags, mask),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	inst := toInstanceJSON(rt.project, rt.location, shortInstanceID(updated.Name), updated)

	raw, mErr := json.Marshal(inst)
	if mErr != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", mErr.Error())
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, h.doneOperation(rt.project, rt.location, "update-"+rt.name, raw))
}

// deleteInstance handles DELETE .../instances/{i} — Delete. The operation
// completes inline, so a done=true Operation with an empty response is returned.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.cache.DeleteCache(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.doneOperation(rt.project, rt.location, "delete-"+rt.name, json.RawMessage("{}"))

	gcprest.WriteJSON(w, http.StatusOK, op)
}
