package memorystore

import (
	"encoding/json"
	"net/http"

	cachedriver "github.com/stackshy/cloudemu/cache/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
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
		Tags:     body.Labels,
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

	op := doneOperation(rt.project, rt.location, "create-"+instanceID, raw)

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

// listInstances handles GET .../instances — List.
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, rt route) {
	infos, err := h.cache.ListCaches(r.Context())
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]instanceJSON, 0, len(infos))
	for i := range infos {
		// CacheInfo.Name carries the driver's own resource id; the short
		// instance id (the map key) is recovered from its trailing segment.
		id := shortInstanceID(infos[i].Name)
		out = append(out, toInstanceJSON(rt.project, rt.location, id, &infos[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listInstancesResponse{Instances: out})
}

// deleteInstance handles DELETE .../instances/{i} — Delete. The operation
// completes inline, so a done=true Operation with an empty response is returned.
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.cache.DeleteCache(r.Context(), rt.name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := doneOperation(rt.project, rt.location, "delete-"+rt.name, json.RawMessage("{}"))

	gcprest.WriteJSON(w, http.StatusOK, op)
}
