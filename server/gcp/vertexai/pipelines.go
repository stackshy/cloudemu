package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// --- Training pipelines ---

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveTrainingPipelines(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listTrainingPipelines(w, r, p.location)
		case http.MethodPost:
			h.createTrainingPipeline(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action == actionCancel {
		if err := h.svc.CancelTrainingPipeline(r.Context(), p.name); err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{})

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTrainingPipeline(w, r, p.name)
	case http.MethodDelete:
		h.deleteTrainingPipeline(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func trainingPipelineJSON(t *driver.TrainingPipeline) map[string]any {
	return map[string]any{
		"name": t.Name, "displayName": t.DisplayName, "state": t.State,
		"createTime": t.CreateTime, "endTime": t.EndTime,
	}
}

func (h *Handler) createTrainingPipeline(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		TaskType    string `json:"taskType"`
	}

	if !decode(w, r, &req) {
		return
	}

	tp, err := h.svc.CreateTrainingPipeline(r.Context(), driver.TrainingPipelineConfig{
		Location: location, DisplayName: req.DisplayName, TaskType: req.TaskType,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, trainingPipelineJSON(tp))
}

func (h *Handler) getTrainingPipeline(w http.ResponseWriter, r *http.Request, name string) {
	tp, err := h.svc.GetTrainingPipeline(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, trainingPipelineJSON(tp))
}

func (h *Handler) listTrainingPipelines(w http.ResponseWriter, r *http.Request, location string) {
	tps, err := h.svc.ListTrainingPipelines(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(tps))
	for i := range tps {
		out = append(out, trainingPipelineJSON(&tps[i]))
	}

	writeJSON(w, map[string]any{"trainingPipelines": out})
}

func (h *Handler) deleteTrainingPipeline(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteTrainingPipeline(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Pipeline jobs ---

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) servePipelineJobs(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listPipelineJobs(w, r, p.location)
		case http.MethodPost:
			h.createPipelineJob(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action == actionCancel {
		if err := h.svc.CancelPipelineJob(r.Context(), p.name); err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{})

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getPipelineJob(w, r, p.name)
	case http.MethodDelete:
		h.deletePipelineJob(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func pipelineJobJSON(j *driver.PipelineJob) map[string]any {
	return map[string]any{
		"name": j.Name, "displayName": j.DisplayName, "state": j.State,
		"createTime": j.CreateTime, "endTime": j.EndTime,
	}
}

func (h *Handler) createPipelineJob(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		TemplateURI string `json:"templateUri"`
	}

	if !decode(w, r, &req) {
		return
	}

	pj, err := h.svc.CreatePipelineJob(r.Context(), driver.PipelineJobConfig{
		Location: location, DisplayName: req.DisplayName, TemplateURI: req.TemplateURI,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, pipelineJobJSON(pj))
}

func (h *Handler) getPipelineJob(w http.ResponseWriter, r *http.Request, name string) {
	pj, err := h.svc.GetPipelineJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, pipelineJobJSON(pj))
}

func (h *Handler) listPipelineJobs(w http.ResponseWriter, r *http.Request, location string) {
	pjs, err := h.svc.ListPipelineJobs(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(pjs))
	for i := range pjs {
		out = append(out, pipelineJobJSON(&pjs[i]))
	}

	writeJSON(w, map[string]any{"pipelineJobs": out})
}

func (h *Handler) deletePipelineJob(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeletePipelineJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}
