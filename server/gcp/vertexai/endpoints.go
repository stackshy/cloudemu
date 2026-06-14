package vertexai

import (
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func endpointJSON(e *driver.Endpoint) map[string]any {
	dms := make([]map[string]any, 0, len(e.DeployedModels))
	for _, d := range e.DeployedModels {
		dms = append(dms, map[string]any{
			"id": d.ID, "model": d.Model, "displayName": d.DisplayName,
		})
	}

	traffic := map[string]any{}
	for k, v := range e.TrafficSplit {
		traffic[k] = v
	}

	return map[string]any{
		"name":           e.Name,
		"displayName":    e.DisplayName,
		"description":    e.Description,
		"deployedModels": dms,
		"trafficSplit":   traffic,
		"labels":         e.Labels,
		"createTime":     e.CreateTime,
		"updateTime":     e.UpdateTime,
	}
}

func (h *Handler) serveEndpoints(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listEndpoints(w, r, p.location)
		case http.MethodPost:
			h.createEndpoint(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action != "" {
		h.endpointAction(w, r, p)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getEndpoint(w, r, p.name)
	case http.MethodDelete:
		h.deleteEndpoint(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) endpointAction(w http.ResponseWriter, r *http.Request, p *vPath) {
	switch p.action {
	case "predict":
		h.predict(w, r, p.name)
	case "rawPredict":
		h.rawPredict(w, r, p.name)
	case "deployModel":
		h.deployModel(w, r, p.name)
	case "undeployModel":
		h.undeployModel(w, r, p.name)
	case actionGenerateContent, actionStreamGenerateContent:
		h.endpointGenerateContent(w, r, p.name)
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown endpoint action: "+p.action)
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string            `json:"displayName"`
		Description string            `json:"description"`
		Labels      map[string]string `json:"labels"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, _, err := h.svc.CreateEndpoint(r.Context(), driver.EndpointConfig{
		Location: location, DisplayName: req.DisplayName, Description: req.Description, Labels: req.Labels,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	ep, err := h.svc.GetEndpoint(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, endpointJSON(ep))
}

func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request, location string) {
	eps, err := h.svc.ListEndpoints(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(eps))
	for i := range eps {
		out = append(out, endpointJSON(&eps[i]))
	}

	writeJSON(w, map[string]any{"endpoints": out})
}

func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteEndpoint(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) deployModel(w http.ResponseWriter, r *http.Request, endpoint string) {
	var req struct {
		DeployedModel struct {
			ID                 string `json:"id"`
			Model              string `json:"model"`
			DisplayName        string `json:"displayName"`
			DedicatedResources struct {
				MachineSpec struct {
					MachineType string `json:"machineType"`
				} `json:"machineSpec"`
				MinReplicaCount int `json:"minReplicaCount"`
				MaxReplicaCount int `json:"maxReplicaCount"`
			} `json:"dedicatedResources"`
		} `json:"deployedModel"`
	}

	if !decode(w, r, &req) {
		return
	}

	dm := req.DeployedModel

	op, _, err := h.svc.DeployModel(r.Context(), endpoint, driver.DeployedModel{
		ID: dm.ID, Model: dm.Model, DisplayName: dm.DisplayName,
		MachineType:     dm.DedicatedResources.MachineSpec.MachineType,
		MinReplicaCount: dm.DedicatedResources.MinReplicaCount,
		MaxReplicaCount: dm.DedicatedResources.MaxReplicaCount,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) undeployModel(w http.ResponseWriter, r *http.Request, endpoint string) {
	var req struct {
		DeployedModelID string `json:"deployedModelId"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, _, err := h.svc.UndeployModel(r.Context(), endpoint, req.DeployedModelID)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) predict(w http.ResponseWriter, r *http.Request, endpoint string) {
	var req struct {
		Instances  []any `json:"instances"`
		Parameters any   `json:"parameters"`
	}

	if !decode(w, r, &req) {
		return
	}

	resp, err := h.svc.Predict(r.Context(), driver.PredictRequest{
		Endpoint: endpoint, Instances: req.Instances, Parameters: req.Parameters,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"predictions":      resp.Predictions,
		"deployedModelId":  resp.DeployedModelID,
		"model":            resp.Model,
		"modelDisplayName": resp.ModelDisplayName,
	})
}

func (h *Handler) rawPredict(w http.ResponseWriter, r *http.Request, endpoint string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "badRequest", "failed to read body")

		return
	}

	out, cerr := h.svc.RawPredict(r.Context(), endpoint, body)
	if cerr != nil {
		writeCErr(w, cerr)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
