package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// --- Featurestores ---

func featurestoreJSON(f *driver.Featurestore) map[string]any {
	return map[string]any{
		"name": f.Name, "state": f.State,
		"onlineServingConfig": map[string]any{"fixedNodeCount": f.OnlineNodeCount},
		"labels":              f.Labels,
		"createTime":          f.CreateTime,
		"updateTime":          f.UpdateTime,
		"etag":                f.Etag,
	}
}

func (h *Handler) serveFeaturestores(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subRes == "entityTypes" {
		h.serveEntityTypes(w, r, p)

		return
	}

	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listFeaturestores(w, r, p.location)
		case http.MethodPost:
			h.createFeaturestore(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getFeaturestore(w, r, p.name)
	case http.MethodPatch:
		h.patchFeaturestore(w, r, p.name)
	case http.MethodDelete:
		h.deleteFeaturestore(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createFeaturestore(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		OnlineServingConfig struct {
			FixedNodeCount int `json:"fixedNodeCount"`
		} `json:"onlineServingConfig"`
		Labels map[string]string `json:"labels"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, fs, err := h.svc.CreateFeaturestore(r.Context(), driver.FeaturestoreConfig{
		Location: location, FeaturestoreID: r.URL.Query().Get("featurestoreId"),
		OnlineNodeCount: req.OnlineServingConfig.FixedNodeCount, Labels: req.Labels,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, featurestoreJSON(fs), "Featurestore")
}

func (h *Handler) patchFeaturestore(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		OnlineServingConfig struct {
			FixedNodeCount int `json:"fixedNodeCount"`
		} `json:"onlineServingConfig"`
		Labels map[string]string `json:"labels"`
	}

	if !decode(w, r, &req) {
		return
	}

	mask := r.URL.Query().Get("updateMask")

	var upd driver.FeaturestoreUpdate
	if maskWants(mask, "onlineServingConfig.fixedNodeCount") || maskWants(mask, "onlineServingConfig") {
		upd.OnlineNodeCount = &req.OnlineServingConfig.FixedNodeCount
	}

	if maskWants(mask, "labels") {
		upd.Labels, upd.SetLabels = req.Labels, true
	}

	op, fs, err := h.svc.PatchFeaturestore(r.Context(), name, upd)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, featurestoreJSON(fs), "Featurestore")
}

func (h *Handler) getFeaturestore(w http.ResponseWriter, r *http.Request, name string) {
	fs, err := h.svc.GetFeaturestore(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, featurestoreJSON(fs))
}

func (h *Handler) listFeaturestores(w http.ResponseWriter, r *http.Request, location string) {
	fss, err := h.svc.ListFeaturestores(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(fss))
	for i := range fss {
		out = append(out, featurestoreJSON(&fss[i]))
	}

	writeJSON(w, map[string]any{"featurestores": out})
}

func (h *Handler) deleteFeaturestore(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteFeaturestore(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Entity types + online feature values ---

func entityTypeJSON(e *driver.EntityType) map[string]any {
	return map[string]any{"name": e.Name, "description": e.Description, "createTime": e.CreateTime}
}

func (h *Handler) serveEntityTypes(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subName == "" {
		switch r.Method {
		case http.MethodGet:
			h.listEntityTypes(w, r, p.name)
		case http.MethodPost:
			h.createEntityType(w, r, p)
		default:
			methodNotAllowed(w)
		}

		return
	}

	name := p.name + "/entityTypes/" + p.subName

	if r.Method == http.MethodPost && p.action != "" {
		h.entityTypeAction(w, r, name, p.action)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getEntityType(w, r, name)
	case http.MethodDelete:
		h.deleteEntityType(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) entityTypeAction(w http.ResponseWriter, r *http.Request, name, action string) {
	switch action {
	case "readFeatureValues":
		h.readFeatureValues(w, r, name)
	case "writeFeatureValues":
		h.writeFeatureValues(w, r, name)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown entityType action: "+action)
	}
}

func (h *Handler) createEntityType(w http.ResponseWriter, r *http.Request, p *vPath) {
	var req struct {
		EntityType struct {
			Description string `json:"description"`
		} `json:"entityType"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, et, err := h.svc.CreateEntityType(r.Context(), p.name, r.URL.Query().Get("entityTypeId"), req.EntityType.Description)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, entityTypeJSON(et), "EntityType")
}

func (h *Handler) getEntityType(w http.ResponseWriter, r *http.Request, name string) {
	et, err := h.svc.GetEntityType(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, entityTypeJSON(et))
}

func (h *Handler) listEntityTypes(w http.ResponseWriter, r *http.Request, parent string) {
	ets, err := h.svc.ListEntityTypes(r.Context(), parent)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ets))
	for i := range ets {
		out = append(out, entityTypeJSON(&ets[i]))
	}

	writeJSON(w, map[string]any{"entityTypes": out})
}

func (h *Handler) deleteEntityType(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteEntityType(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) readFeatureValues(w http.ResponseWriter, r *http.Request, entityType string) {
	var req struct {
		EntityID string `json:"entityId"`
	}

	if !decode(w, r, &req) {
		return
	}

	vals, err := h.svc.ReadFeatureValues(r.Context(), entityType, req.EntityID)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"entityView": map[string]any{"entityId": req.EntityID, "data": featureValuesJSON(vals)}})
}

func (h *Handler) writeFeatureValues(w http.ResponseWriter, r *http.Request, entityType string) {
	var req struct {
		Payloads []struct {
			EntityID      string            `json:"entityId"`
			FeatureValues map[string]string `json:"featureValues"`
		} `json:"payloads"`
	}

	if !decode(w, r, &req) {
		return
	}

	for _, pl := range req.Payloads {
		values := make([]driver.FeatureNameValue, 0, len(pl.FeatureValues))
		for name, val := range pl.FeatureValues {
			values = append(values, driver.FeatureNameValue{Name: name, Value: val})
		}

		if err := h.svc.WriteFeatureValues(r.Context(), entityType, pl.EntityID, values); err != nil {
			writeCErr(w, err)

			return
		}
	}

	writeJSON(w, map[string]any{})
}

func featureValuesJSON(vals []driver.FeatureNameValue) []map[string]any {
	out := make([]map[string]any, 0, len(vals))
	for _, v := range vals {
		out = append(out, map[string]any{"name": v.Name, "value": v.Value})
	}

	return out
}
