package vertexai

import (
	"net/http"

	"github.com/stackshy/cloudemu/vertexai/driver"
)

// --- Hyperparameter tuning jobs ---

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveHyperparameterTuningJobs(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listHyperparameterTuningJobs(w, r, p.location)
		case http.MethodPost:
			h.createHyperparameterTuningJob(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action == actionCancel {
		if err := h.svc.CancelHyperparameterTuningJob(r.Context(), p.name); err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{})

		return
	}

	if r.Method == http.MethodGet {
		h.getHyperparameterTuningJob(w, r, p.name)

		return
	}

	methodNotAllowed(w)
}

func hpoJobJSON(j *driver.HyperparameterTuningJob) map[string]any {
	return map[string]any{
		"name": j.Name, "displayName": j.DisplayName, "state": j.State,
		"maxTrialCount": j.MaxTrialCount, "createTime": j.CreateTime, "endTime": j.EndTime,
	}
}

func (h *Handler) createHyperparameterTuningJob(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		DisplayName   string `json:"displayName"`
		MaxTrialCount int    `json:"maxTrialCount"`
		ParallelTrial int    `json:"parallelTrialCount"`
	}

	if !decode(w, r, &req) {
		return
	}

	job, err := h.svc.CreateHyperparameterTuningJob(r.Context(), driver.HyperparameterTuningJobConfig{
		Location: location, DisplayName: req.DisplayName,
		MaxTrialCount: req.MaxTrialCount, ParallelTrials: req.ParallelTrial,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, hpoJobJSON(job))
}

func (h *Handler) getHyperparameterTuningJob(w http.ResponseWriter, r *http.Request, name string) {
	job, err := h.svc.GetHyperparameterTuningJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, hpoJobJSON(job))
}

func (h *Handler) listHyperparameterTuningJobs(w http.ResponseWriter, r *http.Request, location string) {
	jobs, err := h.svc.ListHyperparameterTuningJobs(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, hpoJobJSON(&jobs[i]))
	}

	writeJSON(w, map[string]any{"hyperparameterTuningJobs": out})
}

// --- Tuning jobs (model tuning) ---

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) serveTuningJobs(w http.ResponseWriter, r *http.Request, p *vPath) {
	if p.name == "" {
		switch r.Method {
		case http.MethodGet:
			h.listTuningJobs(w, r, p.location)
		case http.MethodPost:
			h.createTuningJob(w, r, p.location)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method == http.MethodPost && p.action == actionCancel {
		if err := h.svc.CancelTuningJob(r.Context(), p.name); err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{})

		return
	}

	if r.Method == http.MethodGet {
		h.getTuningJob(w, r, p.name)

		return
	}

	methodNotAllowed(w)
}

func tuningJobJSON(j *driver.TuningJob) map[string]any {
	return map[string]any{
		"name": j.Name, "baseModel": j.BaseModel, "state": j.State,
		"tunedModelDisplayName": j.TunedModelName, "endpoint": j.Endpoint,
		"createTime": j.CreateTime, "endTime": j.EndTime,
	}
}

//nolint:dupl // REST shim; the decode/dispatch shape recurs across collections.
func (h *Handler) createTuningJob(w http.ResponseWriter, r *http.Request, location string) {
	var req struct {
		BaseModel             string `json:"baseModel"`
		TunedModelDisplayName string `json:"tunedModelDisplayName"`
		SupervisedTuningSpec  struct {
			TrainingDatasetURI string `json:"trainingDatasetUri"`
		} `json:"supervisedTuningSpec"`
	}

	if !decode(w, r, &req) {
		return
	}

	job, err := h.svc.CreateTuningJob(r.Context(), driver.TuningJobConfig{
		Location: location, BaseModel: req.BaseModel, TunedModelName: req.TunedModelDisplayName,
		TrainingDataURI: req.SupervisedTuningSpec.TrainingDatasetURI,
	})
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, tuningJobJSON(job))
}

func (h *Handler) getTuningJob(w http.ResponseWriter, r *http.Request, name string) {
	job, err := h.svc.GetTuningJob(r.Context(), name)
	if err != nil {
		writeCErr(w, err)

		return
	}

	writeJSON(w, tuningJobJSON(job))
}

func (h *Handler) listTuningJobs(w http.ResponseWriter, r *http.Request, location string) {
	jobs, err := h.svc.ListTuningJobs(r.Context(), location)
	if err != nil {
		writeCErr(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, tuningJobJSON(&jobs[i]))
	}

	writeJSON(w, map[string]any{"tuningJobs": out})
}
