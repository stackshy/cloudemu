package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

func modelJSON(m *driver.Model) map[string]any {
	return map[string]any{
		"name":           m.Name,
		"displayName":    m.DisplayName,
		"description":    m.Description,
		"versionId":      m.VersionID,
		"versionAliases": m.VersionAliases,
		"artifactUri":    m.ArtifactURI,
		"containerSpec":  map[string]any{"imageUri": m.ContainerImage},
		"labels":         m.Labels,
		"createTime":     m.CreateTime,
		"updateTime":     m.UpdateTime,
	}
}

func (h *Handler) serveModels(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch {
		case r.Method == http.MethodGet:
			h.listModels(w, r, p.location)
		case r.Method == http.MethodPost && p.action == "upload":
			h.uploadModel(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if p.subRes == "evaluations" {
		h.serveModelEvaluations(w, r, p)

		return
	}

	if r.Method == http.MethodGet && p.action == "listVersions" {
		h.listModelVersions(w, r, p.name)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getModel(w, r, p.name)
	case http.MethodDelete:
		h.deleteModel(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func evaluationJSON(e *driver.ModelEvaluation) map[string]any {
	return map[string]any{
		"name": e.Name, "displayName": e.DisplayName,
		"metricsSchemaUri": e.MetricsType, "createTime": e.CreateTime,
	}
}

func (h *Handler) serveModelEvaluations(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.subName == "" {
		switch r.Method {
		case http.MethodGet:
			h.listModelEvaluations(w, r, p.name)
		case http.MethodPost:
			h.importModelEvaluation(w, r, p.name)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	ev, err := h.svc.GetModelEvaluation(r.Context(), p.name+"/evaluations/"+p.subName)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, evaluationJSON(ev))
}

func (h *Handler) importModelEvaluation(w http.ResponseWriter, r *http.Request, modelName string) {
	var req struct {
		DisplayName      string `json:"displayName"`
		MetricsSchemaURI string `json:"metricsSchemaUri"`
	}

	if !decode(w, r, &req) {
		return
	}

	ev, err := h.svc.ImportModelEvaluation(r.Context(), modelName, driver.ModelEvaluation{
		DisplayName: req.DisplayName, MetricsType: req.MetricsSchemaURI,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, evaluationJSON(ev))
}

func (h *Handler) listModelEvaluations(w http.ResponseWriter, r *http.Request, modelName string) {
	evs, err := h.svc.ListModelEvaluations(r.Context(), modelName)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(evs))
	for i := range evs {
		out = append(out, evaluationJSON(&evs[i]))
	}

	writeJSON(w, map[string]any{"modelEvaluations": out})
}

func (h *Handler) listModelVersions(w http.ResponseWriter, r *http.Request, name string) {
	models, err := h.svc.ListModelVersions(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(models))
	for i := range models {
		out = append(out, modelJSON(&models[i]))
	}

	writeJSON(w, map[string]any{"models": out})
}

func (h *Handler) uploadModel(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		Model struct {
			DisplayName   string `json:"displayName"`
			Description   string `json:"description"`
			ArtifactURI   string `json:"artifactUri"`
			ContainerSpec struct {
				ImageURI string `json:"imageUri"`
			} `json:"containerSpec"`
			Labels map[string]string `json:"labels"`
		} `json:"model"`
	}

	if !decode(w, r, &req) {
		return
	}

	op, _, err := h.svc.UploadModel(r.Context(), driver.ModelConfig{
		Location:       location,
		DisplayName:    req.Model.DisplayName,
		Description:    req.Model.Description,
		ContainerImage: req.Model.ContainerSpec.ImageURI,
		ArtifactURI:    req.Model.ArtifactURI,
		Labels:         req.Model.Labels,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

func (h *Handler) getModel(w http.ResponseWriter, r *http.Request, name string) {
	model, err := h.svc.GetModel(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, modelJSON(model))
}

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request, location string) {
	models, err := h.svc.ListModels(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(models))
	for i := range models {
		out = append(out, modelJSON(&models[i]))
	}

	writeJSON(w, map[string]any{"models": out})
}

func (h *Handler) deleteModel(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteModel(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}
