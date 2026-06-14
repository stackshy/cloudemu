package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// --- Custom jobs ---

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveCustomJobs(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listCustomJobs(w, r, p.location)
		case http.MethodPost:
			h.createCustomJob(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action == actionCancel {
		if err := h.svc.CancelCustomJob(r.Context(), p.name); err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{})

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCustomJob(w, r, p.name)
	case http.MethodDelete:
		h.deleteCustomJob(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func customJobJSON(j *driver.CustomJob) map[string]any {
	return map[string]any{
		"name": j.Name, "displayName": j.DisplayName, "state": j.State,
		"createTime": j.CreateTime, "endTime": j.EndTime,
	}
}

func (h *Handler) createCustomJob(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
	}

	if !decode(w, r, &req) {
		return
	}

	job, err := h.svc.CreateCustomJob(r.Context(), driver.CustomJobConfig{Location: location, DisplayName: req.DisplayName})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, customJobJSON(job))
}

func (h *Handler) getCustomJob(w http.ResponseWriter, r *http.Request, name string) {
	job, err := h.svc.GetCustomJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, customJobJSON(job))
}

func (h *Handler) listCustomJobs(w http.ResponseWriter, r *http.Request, location string) {
	jobs, err := h.svc.ListCustomJobs(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, customJobJSON(&jobs[i]))
	}

	writeJSON(w, map[string]any{"customJobs": out})
}

func (h *Handler) deleteCustomJob(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteCustomJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}

// --- Batch prediction jobs ---

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveBatchPredictionJobs(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listBatchPredictionJobs(w, r, p.location)
		case http.MethodPost:
			h.createBatchPredictionJob(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action == actionCancel {
		if err := h.svc.CancelBatchPredictionJob(r.Context(), p.name); err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{})

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBatchPredictionJob(w, r, p.name)
	case http.MethodDelete:
		h.deleteBatchPredictionJob(w, r, p.name)
	default:
		methodNotAllowed(w)
	}
}

func batchJobJSON(j *driver.BatchPredictionJob) map[string]any {
	return map[string]any{
		"name": j.Name, "displayName": j.DisplayName, "model": j.Model, "state": j.State,
		"createTime": j.CreateTime, "endTime": j.EndTime,
	}
}

func (h *Handler) createBatchPredictionJob(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName string `json:"displayName"`
		Model       string `json:"model"`
	}

	if !decode(w, r, &req) {
		return
	}

	job, err := h.svc.CreateBatchPredictionJob(r.Context(), driver.BatchPredictionJobConfig{
		Location: location, DisplayName: req.DisplayName, Model: req.Model,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, batchJobJSON(job))
}

func (h *Handler) getBatchPredictionJob(w http.ResponseWriter, r *http.Request, name string) {
	job, err := h.svc.GetBatchPredictionJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, batchJobJSON(job))
}

func (h *Handler) listBatchPredictionJobs(w http.ResponseWriter, r *http.Request, location string) {
	jobs, err := h.svc.ListBatchPredictionJobs(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, batchJobJSON(&jobs[i]))
	}

	writeJSON(w, map[string]any{"batchPredictionJobs": out})
}

func (h *Handler) deleteBatchPredictionJob(w http.ResponseWriter, r *http.Request, name string) {
	op, err := h.svc.DeleteBatchPredictionJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeOp(w, op)
}
