package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// --- Feature groups + features (Feature Registry) ---

func featureGroupJSON(g *driver.FeatureGroup) map[string]any {
	return map[string]any{
		"name": g.Name, "description": g.Description,
		"bigQuery":   map[string]any{"bigQuerySource": map[string]any{"inputUri": g.BigQueryURI}},
		"createTime": g.CreateTime,
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveFeatureGroups(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subRes == "features" {
		h.serveFeatures(w, r, p)

		return
	}

	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listFeatureGroups(w, r, p.location)
		case http.MethodPost:
			h.createFeatureGroup(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getFeatureGroup(w, r, p.name)
	case http.MethodDelete:
		h.deleteFeatureGroup(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createFeatureGroup(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		Description string `json:"description"`
		BigQuery    struct {
			BigQuerySource struct {
				InputURI string `json:"inputUri"`
			} `json:"bigQuerySource"`
		} `json:"bigQuery"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, fg, err := h.svc.CreateFeatureGroup(r.Context(), driver.FeatureGroupConfig{
		Location: location, FeatureGroupID: r.URL.Query().Get("featureGroupId"),
		Description: req.Description, BigQueryURI: req.BigQuery.BigQuerySource.InputURI,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, featureGroupJSON(fg), "FeatureGroup")
}

func (h *Handler) getFeatureGroup(w http.ResponseWriter, r *http.Request, name string) {
	g, err := h.svc.GetFeatureGroup(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, featureGroupJSON(g))
}

func (h *Handler) listFeatureGroups(w http.ResponseWriter, r *http.Request, location string) {
	gs, err := h.svc.ListFeatureGroups(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(gs))
	for i := range gs {
		out = append(out, featureGroupJSON(&gs[i]))
	}

	writeJSON(w, map[string]any{"featureGroups": out})
}

func (h *Handler) deleteFeatureGroup(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteFeatureGroup(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func featureJSON(f *driver.Feature) map[string]any {
	return map[string]any{"name": f.Name, "description": f.Description, "createTime": f.CreateTime}
}

func (h *Handler) serveFeatures(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subName == "" {
		switch r.Method {
		case http.MethodGet:
			h.listFeatures(w, r, p.name)
		case http.MethodPost:
			h.createFeature(w, r, p)
		default:
			methodNotAllowed(w)
		}

		return
	}

	name := p.name + "/features/" + p.subName

	switch r.Method {
	case http.MethodGet:
		h.getFeature(w, r, name)
	case http.MethodDelete:
		h.deleteFeature(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createFeature(w http.ResponseWriter, r *http.Request, p *vPath) {
	var req struct {
		Description string `json:"description"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, f, err := h.svc.CreateFeature(r.Context(), p.name, r.URL.Query().Get("featureId"), req.Description)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, featureJSON(f), "Feature")
}

func (h *Handler) getFeature(w http.ResponseWriter, r *http.Request, name string) {
	f, err := h.svc.GetFeature(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, featureJSON(f))
}

func (h *Handler) listFeatures(w http.ResponseWriter, r *http.Request, parent string) {
	fs, err := h.svc.ListFeatures(r.Context(), parent)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(fs))
	for i := range fs {
		out = append(out, featureJSON(&fs[i]))
	}

	writeJSON(w, map[string]any{"features": out})
}

func (h *Handler) deleteFeature(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteFeature(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Feature online stores + feature views ---

func onlineStoreJSON(s *driver.FeatureOnlineStore) map[string]any {
	return map[string]any{"name": s.Name, "state": s.State, "createTime": s.CreateTime}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveFeatureOnlineStores(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subRes == "featureViews" {
		h.serveFeatureViews(w, r, p)

		return
	}

	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listFeatureOnlineStores(w, r, p.location)
		case http.MethodPost:
			h.createFeatureOnlineStore(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getFeatureOnlineStore(w, r, p.name)
	case http.MethodDelete:
		h.deleteFeatureOnlineStore(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createFeatureOnlineStore(w http.ResponseWriter, r *http.Request, location string) {
	if !decode(w, r, &struct{}{}) {
		return
	}

	op, s, err := h.svc.CreateFeatureOnlineStore(r.Context(), driver.FeatureOnlineStoreConfig{
		Location: location, FeatureOnlineStoreID: r.URL.Query().Get("featureOnlineStoreId"),
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, onlineStoreJSON(s), "FeatureOnlineStore")
}

func (h *Handler) getFeatureOnlineStore(w http.ResponseWriter, r *http.Request, name string) {
	s, err := h.svc.GetFeatureOnlineStore(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, onlineStoreJSON(s))
}

func (h *Handler) listFeatureOnlineStores(w http.ResponseWriter, r *http.Request, location string) {
	ss, err := h.svc.ListFeatureOnlineStores(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(ss))
	for i := range ss {
		out = append(out, onlineStoreJSON(&ss[i]))
	}

	writeJSON(w, map[string]any{"featureOnlineStores": out})
}

func (h *Handler) deleteFeatureOnlineStore(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteFeatureOnlineStore(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func featureViewJSON(v *driver.FeatureView) map[string]any {
	return map[string]any{
		"name":           v.Name,
		"bigQuerySource": map[string]any{"uri": v.BigQueryURI},
		"createTime":     v.CreateTime,
	}
}

func (h *Handler) serveFeatureViews(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subName == "" {
		switch r.Method {
		case http.MethodGet:
			h.listFeatureViews(w, r, p.name)
		case http.MethodPost:
			h.createFeatureView(w, r, p)
		default:
			methodNotAllowed(w)
		}

		return
	}

	name := p.name + "/featureViews/" + p.subName

	if r.Method == http.MethodPost && p.action == "fetchFeatureValues" {
		h.fetchFeatureValues(w, r, name)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getFeatureView(w, r, name)
	case http.MethodDelete:
		h.deleteFeatureView(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createFeatureView(w http.ResponseWriter, r *http.Request, p *vPath) {
	var req struct {
		BigQuerySource struct {
			URI string `json:"uri"`
		} `json:"bigQuerySource"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, fv, err := h.svc.CreateFeatureView(r.Context(), driver.FeatureViewConfig{
		Parent: p.name, FeatureViewID: r.URL.Query().Get("featureViewId"),
		BigQueryURI: req.BigQuerySource.URI,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, featureViewJSON(fv), "FeatureView")
}

func (h *Handler) getFeatureView(w http.ResponseWriter, r *http.Request, name string) {
	v, err := h.svc.GetFeatureView(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, featureViewJSON(v))
}

func (h *Handler) listFeatureViews(w http.ResponseWriter, r *http.Request, parent string) {
	vs, err := h.svc.ListFeatureViews(r.Context(), parent)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(vs))
	for i := range vs {
		out = append(out, featureViewJSON(&vs[i]))
	}

	writeJSON(w, map[string]any{"featureViews": out})
}

func (h *Handler) deleteFeatureView(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteFeatureView(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) fetchFeatureValues(w http.ResponseWriter, r *http.Request, featureView string) {
	var req struct {
		DataKey struct {
			Key string `json:"key"`
		} `json:"dataKey"`
	}

	if !decode(w, r, &req) {
		return
	}

	vals, err := h.svc.FetchFeatureValues(r.Context(), featureView, req.DataKey.Key)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"keyValues": map[string]any{"features": featureValuesJSON(vals)}})
}
