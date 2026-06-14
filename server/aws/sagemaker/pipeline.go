package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

func (h *Handler) routePipelines(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreatePipeline":
		h.createPipeline(w, r)
	case "DescribePipeline":
		h.describePipeline(w, r)
	case "ListPipelines":
		h.listPipelines(w, r)
	case "UpdatePipeline":
		h.updatePipeline(w, r)
	case "DeletePipeline":
		h.deletePipeline(w, r)
	case "StartPipelineExecution":
		h.startPipelineExecution(w, r)
	case "DescribePipelineExecution":
		h.describePipelineExecution(w, r)
	case "ListPipelineExecutions":
		h.listPipelineExecutions(w, r)
	case "StopPipelineExecution":
		h.stopPipelineExecution(w, r)
	default:
		return h.routePipelinesMeta(w, r, op)
	}

	return true
}

func (h *Handler) routePipelinesMeta(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateExperiment":
		h.createExperiment(w, r)
	case "DescribeExperiment":
		h.describeExperiment(w, r)
	case "ListExperiments":
		h.listExperiments(w, r)
	case "DeleteExperiment":
		h.deleteExperiment(w, r)
	case "CreateTrial":
		h.createTrial(w, r)
	case "DescribeTrial":
		h.describeTrial(w, r)
	case "ListTrials":
		h.listTrials(w, r)
	case "DeleteTrial":
		h.deleteTrial(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) createPipeline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PipelineName       string    `json:"PipelineName"`
		RoleArn            string    `json:"RoleArn"`
		PipelineDefinition string    `json:"PipelineDefinition"`
		Tags               []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.svc.CreatePipeline(r.Context(), driver.PipelineSpec{
		PipelineName: req.PipelineName,
		RoleARN:      req.RoleArn,
		Definition:   req.PipelineDefinition,
		Tags:         toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"PipelineArn": p.PipelineARN})
}

func (h *Handler) describePipeline(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "PipelineName")
	if !ok {
		return
	}

	p, err := h.svc.DescribePipeline(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"PipelineName":     p.PipelineName,
		"PipelineArn":      p.PipelineARN,
		"PipelineStatus":   p.Status,
		"RoleArn":          p.RoleARN,
		"CreationTime":     epoch(p.CreationTime),
		"LastModifiedTime": epoch(p.LastModifiedTime),
	})
}

func (h *Handler) listPipelines(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.svc.ListPipelines(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(pipelines))
	for i := range pipelines {
		out = append(out, map[string]any{
			"PipelineName": pipelines[i].PipelineName,
			"PipelineArn":  pipelines[i].PipelineARN,
			"CreationTime": epoch(pipelines[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"PipelineSummaries": out})
}

func (h *Handler) updatePipeline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PipelineName       string `json:"PipelineName"`
		PipelineDefinition string `json:"PipelineDefinition"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	p, err := h.svc.UpdatePipeline(r.Context(), req.PipelineName, req.PipelineDefinition)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"PipelineArn": p.PipelineARN})
}

func (h *Handler) deletePipeline(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "PipelineName")
	if !ok {
		return
	}

	if err := h.svc.DeletePipeline(r.Context(), name); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"PipelineArn": ""})
}

func (h *Handler) startPipelineExecution(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "PipelineName")
	if !ok {
		return
	}

	ex, err := h.svc.StartPipelineExecution(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"PipelineExecutionArn": ex.ExecutionARN})
}

func (h *Handler) describePipelineExecution(w http.ResponseWriter, r *http.Request) {
	arn, ok := decodeName1(w, r, "PipelineExecutionArn")
	if !ok {
		return
	}

	ex, err := h.svc.DescribePipelineExecution(r.Context(), arn)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"PipelineExecutionArn":    ex.ExecutionARN,
		"PipelineExecutionStatus": ex.Status,
		"CreationTime":            epoch(ex.StartTime),
	})
}

func (h *Handler) listPipelineExecutions(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "PipelineName")
	if !ok {
		return
	}

	exs, err := h.svc.ListPipelineExecutions(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(exs))
	for i := range exs {
		out = append(out, map[string]any{
			"PipelineExecutionArn":    exs[i].ExecutionARN,
			"PipelineExecutionStatus": exs[i].Status,
			"StartTime":               epoch(exs[i].StartTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"PipelineExecutionSummaries": out})
}

func (h *Handler) stopPipelineExecution(w http.ResponseWriter, r *http.Request) {
	arn, ok := decodeName1(w, r, "PipelineExecutionArn")
	if !ok {
		return
	}

	if err := h.svc.StopPipelineExecution(r.Context(), arn); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"PipelineExecutionArn": arn})
}

func (h *Handler) createExperiment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExperimentName string    `json:"ExperimentName"`
		Description    string    `json:"Description"`
		Tags           []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	e, err := h.svc.CreateExperiment(r.Context(), driver.ExperimentSpec{
		ExperimentName: req.ExperimentName,
		Description:    req.Description,
		Tags:           toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ExperimentArn": e.ExperimentARN})
}

func (h *Handler) describeExperiment(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ExperimentName")
	if !ok {
		return
	}

	e, err := h.svc.DescribeExperiment(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"ExperimentName": e.ExperimentName,
		"ExperimentArn":  e.ExperimentARN,
		"Description":    e.Description,
		"CreationTime":   epoch(e.CreationTime),
	})
}

func (h *Handler) listExperiments(w http.ResponseWriter, r *http.Request) {
	exps, err := h.svc.ListExperiments(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(exps))
	for i := range exps {
		out = append(out, map[string]any{
			"ExperimentName": exps[i].ExperimentName,
			"ExperimentArn":  exps[i].ExperimentARN,
			"CreationTime":   epoch(exps[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"ExperimentSummaries": out})
}

func (h *Handler) deleteExperiment(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ExperimentName")
	if !ok {
		return
	}

	if err := h.svc.DeleteExperiment(r.Context(), name); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ExperimentArn": ""})
}

func (h *Handler) createTrial(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrialName      string    `json:"TrialName"`
		ExperimentName string    `json:"ExperimentName"`
		Tags           []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	t, err := h.svc.CreateTrial(r.Context(), driver.TrialSpec{
		TrialName:      req.TrialName,
		ExperimentName: req.ExperimentName,
		Tags:           toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrialArn": t.TrialARN})
}

func (h *Handler) describeTrial(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "TrialName")
	if !ok {
		return
	}

	t, err := h.svc.DescribeTrial(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"TrialName":      t.TrialName,
		"TrialArn":       t.TrialARN,
		"ExperimentName": t.ExperimentName,
		"CreationTime":   epoch(t.CreationTime),
	})
}

func (h *Handler) listTrials(w http.ResponseWriter, r *http.Request) {
	expName, ok := decodeName1(w, r, "ExperimentName")
	if !ok {
		return
	}

	trials, err := h.svc.ListTrials(r.Context(), expName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(trials))
	for i := range trials {
		out = append(out, map[string]any{
			"TrialName":    trials[i].TrialName,
			"TrialArn":     trials[i].TrialARN,
			"CreationTime": epoch(trials[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"TrialSummaries": out})
}

func (h *Handler) deleteTrial(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "TrialName")
	if !ok {
		return
	}

	if err := h.svc.DeleteTrial(r.Context(), name); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrialArn": ""})
}
