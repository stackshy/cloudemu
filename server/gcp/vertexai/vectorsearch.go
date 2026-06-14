package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// --- Indexes ---

func indexJSON(i *driver.Index) map[string]any {
	return map[string]any{
		"name": i.Name, "displayName": i.DisplayName, "description": i.Description,
		"indexStats": map[string]any{"vectorsCount": i.DatapointCount},
		"createTime": i.CreateTime, "updateTime": i.UpdateTime,
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveIndexes(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listIndexes(w, r, p.location)
		case http.MethodPost:
			h.createIndex(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action != "" {
		h.indexAction(w, r, p)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getIndex(w, r, p.name)
	case http.MethodDelete:
		h.deleteIndex(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) indexAction(w http.ResponseWriter, r *http.Request, p *vPath) {
	switch p.action {
	case "upsertDatapoints":
		h.upsertDatapoints(w, r, p.name)
	case "removeDatapoints":
		h.removeDatapoints(w, r, p.name)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown index action: "+p.action)
	}
}

func (h *Handler) createIndex(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
		Metadata    struct {
			Config struct {
				Dimensions int `json:"dimensions"`
			} `json:"config"`
		} `json:"metadata"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, idx, err := h.svc.CreateIndex(r.Context(), driver.IndexConfig{
		Location: location, DisplayName: req.DisplayName, Description: req.Description,
		Dimensions: req.Metadata.Config.Dimensions,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, indexJSON(idx), "Index")
}

func (h *Handler) getIndex(w http.ResponseWriter, r *http.Request, name string) {
	idx, err := h.svc.GetIndex(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, indexJSON(idx))
}

func (h *Handler) listIndexes(w http.ResponseWriter, r *http.Request, location string) {
	idxs, err := h.svc.ListIndexes(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(idxs))
	for i := range idxs {
		out = append(out, indexJSON(&idxs[i]))
	}

	writeJSON(w, map[string]any{"indexes": out})
}

func (h *Handler) deleteIndex(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteIndex(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) upsertDatapoints(w http.ResponseWriter, r *http.Request, index string) {
	var req struct {
		Datapoints []struct {
			DatapointID   string    `json:"datapointId"`
			FeatureVector []float64 `json:"featureVector"`
		} `json:"datapoints"`
	}

	if !decode(w, r, &req) {
		return
	}

	dps := make([]driver.Datapoint, 0, len(req.Datapoints))
	for _, d := range req.Datapoints {
		dps = append(dps, driver.Datapoint{DatapointID: d.DatapointID, FeatureVector: d.FeatureVector})
	}

	if err := h.svc.UpsertDatapoints(r.Context(), index, dps); err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) removeDatapoints(w http.ResponseWriter, r *http.Request, index string) {
	var req struct {
		DatapointIDs []string `json:"datapointIds"`
	}

	if !decode(w, r, &req) {
		return
	}

	if err := h.svc.RemoveDatapoints(r.Context(), index, req.DatapointIDs); err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

// --- Index endpoints ---

func indexEndpointJSON(e *driver.IndexEndpoint) map[string]any {
	dis := make([]map[string]any, 0, len(e.DeployedIndexes))
	for _, d := range e.DeployedIndexes {
		dis = append(dis, map[string]any{"id": d.ID, "index": d.Index})
	}

	return map[string]any{
		"name": e.Name, "displayName": e.DisplayName, "description": e.Description,
		"deployedIndexes": dis, "createTime": e.CreateTime,
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveIndexEndpoints(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listIndexEndpoints(w, r, p.location)
		case http.MethodPost:
			h.createIndexEndpoint(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action != "" {
		h.indexEndpointAction(w, r, p)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getIndexEndpoint(w, r, p.name)
	case http.MethodDelete:
		h.deleteIndexEndpoint(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) indexEndpointAction(w http.ResponseWriter, r *http.Request, p *vPath) {
	switch p.action {
	case "deployIndex":
		h.deployIndex(w, r, p.name)
	case "undeployIndex":
		h.undeployIndex(w, r, p.name)
	case "findNeighbors":
		h.findNeighbors(w, r, p.name)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown indexEndpoint action: "+p.action)
	}
}

func (h *Handler) createIndexEndpoint(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, ie, err := h.svc.CreateIndexEndpoint(r.Context(), driver.IndexEndpointConfig{
		Location: location, DisplayName: req.DisplayName, Description: req.Description,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, indexEndpointJSON(ie), "IndexEndpoint")
}

func (h *Handler) getIndexEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	ie, err := h.svc.GetIndexEndpoint(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, indexEndpointJSON(ie))
}

func (h *Handler) listIndexEndpoints(w http.ResponseWriter, r *http.Request, location string) {
	ies, err := h.svc.ListIndexEndpoints(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ies))
	for i := range ies {
		out = append(out, indexEndpointJSON(&ies[i]))
	}

	writeJSON(w, map[string]any{"indexEndpoints": out})
}

func (h *Handler) deleteIndexEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteIndexEndpoint(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) deployIndex(w http.ResponseWriter, r *http.Request, indexEndpoint string) {
	var req struct {
		DeployedIndex struct {
			ID    string `json:"id"`
			Index string `json:"index"`
		} `json:"deployedIndex"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, ie, err := h.svc.DeployIndex(r.Context(), indexEndpoint, driver.DeployedIndex{
		ID: req.DeployedIndex.ID, Index: req.DeployedIndex.Index,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	// DeployIndexResponse carries the newly deployed index (the last appended).
	var deployed map[string]any

	if n := len(ie.DeployedIndexes); n > 0 {
		d := ie.DeployedIndexes[n-1]
		deployed = map[string]any{"id": d.ID, "index": d.Index}
	}

	writeResourceOp(w, op, map[string]any{"deployedIndex": deployed}, "DeployIndexResponse")
}

func (h *Handler) undeployIndex(w http.ResponseWriter, r *http.Request, indexEndpoint string) {
	var req struct {
		DeployedIndexID string `json:"deployedIndexId"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, _, err := h.svc.UndeployIndex(r.Context(), indexEndpoint, req.DeployedIndexID)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, nil, "UndeployIndexResponse")
}

func (h *Handler) findNeighbors(w http.ResponseWriter, r *http.Request, indexEndpoint string) {
	var req struct {
		DeployedIndexID string `json:"deployedIndexId"`
		Queries         []struct {
			Datapoint struct {
				FeatureVector []float64 `json:"featureVector"`
			} `json:"datapoint"`
			NeighborCount int `json:"neighborCount"`
		} `json:"queries"`
	}

	if !decode(w, r, &req) {
		return
	}

	var (
		query []float64
		count int
	)

	if len(req.Queries) > 0 {
		query = req.Queries[0].Datapoint.FeatureVector
		count = req.Queries[0].NeighborCount
	}

	neighbors, err := h.svc.FindNeighbors(r.Context(), indexEndpoint, req.DeployedIndexID, query, count)
	if err != nil {
		writeCErr(w, err)

		return
	}

	nn := make([]map[string]any, 0, len(neighbors))
	for _, n := range neighbors {
		nn = append(nn, map[string]any{
			"datapoint": map[string]any{"datapointId": n.DatapointID}, "distance": n.Distance,
		})
	}

	writeJSON(w, map[string]any{"nearestNeighbors": []map[string]any{{"id": "0", "neighbors": nn}}})
}
