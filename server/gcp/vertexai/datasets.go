package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func datasetJSON(d *driver.Dataset) map[string]any {
	return map[string]any{
		"name":              d.Name,
		"displayName":       d.DisplayName,
		"metadataSchemaUri": d.MetadataSchemaURI,
		"labels":            d.Labels,
		"createTime":        d.CreateTime,
		"updateTime":        d.UpdateTime,
	}
}

func (h *Handler) serveDatasets(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listDatasets(w, r, p.location)
		case http.MethodPost:
			h.createDataset(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action != "" {
		h.datasetAction(w, r, p)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getDataset(w, r, p.name)
	case http.MethodPatch:
		h.patchDataset(w, r, p.name)
	case http.MethodDelete:
		h.deleteDataset(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) datasetAction(w http.ResponseWriter, r *http.Request, p *vPath) {
	var op *driver.Operation

	var err error

	switch p.action {
	case "import":
		op, err = h.svc.ImportData(r.Context(), p.name, "")
	case "export":
		op, err = h.svc.ExportData(r.Context(), p.name, "")
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown dataset action: "+p.action)

		return
	}

	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) createDataset(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName       string            `json:"displayName"`
		MetadataSchemaURI string            `json:"metadataSchemaUri"`
		Labels            map[string]string `json:"labels"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, ds, err := h.svc.CreateDataset(r.Context(), driver.DatasetConfig{
		Location: location, DisplayName: req.DisplayName,
		MetadataSchemaURI: req.MetadataSchemaURI, Labels: req.Labels,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeResourceOp(w, op, datasetJSON(ds), "Dataset")
}

func (h *Handler) getDataset(w http.ResponseWriter, r *http.Request, name string) {
	ds, err := h.svc.GetDataset(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, datasetJSON(ds))
}

func (h *Handler) listDatasets(w http.ResponseWriter, r *http.Request, location string) {
	dss, err := h.svc.ListDatasets(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(dss))
	for i := range dss {
		out = append(out, datasetJSON(&dss[i]))
	}

	writeJSON(w, map[string]any{"datasets": out})
}

func (h *Handler) patchDataset(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		DisplayName string `json:"displayName"`
	}

	if !decode(w, r, &req) {
		return
	}

	ds, err := h.svc.PatchDataset(r.Context(), name, req.DisplayName)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, datasetJSON(ds))
}

func (h *Handler) deleteDataset(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteDataset(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}
