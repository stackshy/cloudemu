package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type workflowJSON struct {
	Name                 string            `json:"Name"`
	Description          string            `json:"Description,omitempty"`
	DefaultRunProperties map[string]string `json:"DefaultRunProperties,omitempty"`
	MaxConcurrentRuns    int32             `json:"MaxConcurrentRuns,omitempty"`
	CreatedOn            *float64          `json:"CreatedOn,omitempty"`
	LastModifiedOn       *float64          `json:"LastModifiedOn,omitempty"`
}

func workflowToWire(wf *driver.Workflow) workflowJSON {
	return workflowJSON{
		Name: wf.Name, Description: wf.Description, DefaultRunProperties: wf.DefaultRunProperties,
		MaxConcurrentRuns: wf.MaxConcurrentRuns, CreatedOn: epochOrNil(wf.CreatedOn),
		LastModifiedOn: epochOrNil(wf.LastModifiedOn),
	}
}

type createWorkflowRequest struct {
	Name                 string            `json:"Name"`
	Description          string            `json:"Description"`
	DefaultRunProperties map[string]string `json:"DefaultRunProperties"`
	MaxConcurrentRuns    int32             `json:"MaxConcurrentRuns"`
}

func (h *Handler) createWorkflow(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createWorkflowRequest) (any, error) {
		name, err := h.glue.CreateWorkflow(ctx, driver.Workflow{
			Name: req.Name, Description: req.Description, DefaultRunProperties: req.DefaultRunProperties,
			MaxConcurrentRuns: req.MaxConcurrentRuns,
		})
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type workflowNameRequest struct {
	Name string `json:"Name"`
}

type getWorkflowResponse struct {
	Workflow workflowJSON `json:"Workflow"`
}

func (h *Handler) getWorkflow(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *workflowNameRequest) (any, error) {
		wf, err := h.glue.GetWorkflow(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getWorkflowResponse{Workflow: workflowToWire(wf)}, nil
	})
}

func (h *Handler) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createWorkflowRequest) (any, error) {
		name, err := h.glue.UpdateWorkflow(ctx, req.Name, driver.Workflow{
			Name: req.Name, Description: req.Description, DefaultRunProperties: req.DefaultRunProperties,
			MaxConcurrentRuns: req.MaxConcurrentRuns,
		})
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

func (h *Handler) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *workflowNameRequest) (any, error) {
		name, err := h.glue.DeleteWorkflow(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type listWorkflowsResponse struct {
	Workflows []string `json:"Workflows"`
	NextToken string   `json:"NextToken,omitempty"`
}

func (h *Handler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		names, next, err := h.glue.ListWorkflows(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		return listWorkflowsResponse{Workflows: names, NextToken: next}, nil
	})
}

type batchGetWorkflowsRequest struct {
	Names []string `json:"Names"`
}

type batchGetWorkflowsResponse struct {
	Workflows        []workflowJSON `json:"Workflows"`
	MissingWorkflows []string       `json:"MissingWorkflows,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) batchGetWorkflows(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetWorkflowsRequest) (any, error) {
		found, missing, err := h.glue.BatchGetWorkflows(ctx, req.Names)
		if err != nil {
			return nil, err
		}

		out := make([]workflowJSON, 0, len(found))
		for i := range found {
			out = append(out, workflowToWire(&found[i]))
		}

		return batchGetWorkflowsResponse{Workflows: out, MissingWorkflows: missing}, nil
	})
}

type startWorkflowRunRequest struct {
	Name string `json:"Name"`
}

type startWorkflowRunResponse struct {
	RunID string `json:"RunId"`
}

func (h *Handler) startWorkflowRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startWorkflowRunRequest) (any, error) {
		id, err := h.glue.StartWorkflowRun(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return startWorkflowRunResponse{RunID: id}, nil
	})
}

type workflowRunJSON struct {
	Name                  string            `json:"Name,omitempty"`
	WorkflowRunID         string            `json:"WorkflowRunID,omitempty"`
	Status                string            `json:"Status,omitempty"`
	StartedOn             *float64          `json:"StartedOn,omitempty"`
	CompletedOn           *float64          `json:"CompletedOn,omitempty"`
	WorkflowRunProperties map[string]string `json:"WorkflowRunProperties,omitempty"`
}

func workflowRunToWire(wr *driver.WorkflowRun) workflowRunJSON {
	return workflowRunJSON{
		Name: wr.Name, WorkflowRunID: wr.WorkflowRunID, Status: wr.Status,
		StartedOn: epochOrNil(wr.StartedOn), CompletedOn: epochOrNil(wr.CompletedOn),
		WorkflowRunProperties: wr.RunProperties,
	}
}

type getWorkflowRunRequest struct {
	Name  string `json:"Name"`
	RunID string `json:"RunId"`
}

type getWorkflowRunResponse struct {
	Run workflowRunJSON `json:"Run"`
}

func (h *Handler) getWorkflowRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getWorkflowRunRequest) (any, error) {
		wr, err := h.glue.GetWorkflowRun(ctx, req.Name, req.RunID)
		if err != nil {
			return nil, err
		}

		return getWorkflowRunResponse{Run: workflowRunToWire(wr)}, nil
	})
}

type getWorkflowRunsRequest struct {
	Name       string `json:"Name"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type getWorkflowRunsResponse struct {
	Runs      []workflowRunJSON `json:"Runs"`
	NextToken string            `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getWorkflowRunsRequest) (any, error) {
		runs, next, err := h.glue.GetWorkflowRuns(ctx, req.Name,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]workflowRunJSON, 0, len(runs))
		for i := range runs {
			out = append(out, workflowRunToWire(&runs[i]))
		}

		return getWorkflowRunsResponse{Runs: out, NextToken: next}, nil
	})
}

func (h *Handler) stopWorkflowRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getWorkflowRunRequest) (any, error) {
		if err := h.glue.StopWorkflowRun(ctx, req.Name, req.RunID); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type resumeWorkflowRunRequest struct {
	Name    string   `json:"Name"`
	RunID   string   `json:"RunId"`
	NodeIDs []string `json:"NodeIds"`
}

type resumeWorkflowRunResponse struct {
	RunID   string   `json:"RunId"`
	NodeIDs []string `json:"NodeIDs,omitempty"`
}

func (h *Handler) resumeWorkflowRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *resumeWorkflowRunRequest) (any, error) {
		id, err := h.glue.ResumeWorkflowRun(ctx, req.Name, req.RunID, req.NodeIDs)
		if err != nil {
			return nil, err
		}

		return resumeWorkflowRunResponse{RunID: id, NodeIDs: req.NodeIDs}, nil
	})
}

type getWorkflowRunPropertiesResponse struct {
	RunProperties map[string]string `json:"RunProperties"`
}

func (h *Handler) getWorkflowRunProperties(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getWorkflowRunRequest) (any, error) {
		props, err := h.glue.GetWorkflowRunProperties(ctx, req.Name, req.RunID)
		if err != nil {
			return nil, err
		}

		return getWorkflowRunPropertiesResponse{RunProperties: props}, nil
	})
}

type putWorkflowRunPropertiesRequest struct {
	Name          string            `json:"Name"`
	RunID         string            `json:"RunId"`
	RunProperties map[string]string `json:"RunProperties"`
}

func (h *Handler) putWorkflowRunProperties(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putWorkflowRunPropertiesRequest) (any, error) {
		if err := h.glue.PutWorkflowRunProperties(ctx, req.Name, req.RunID, req.RunProperties); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
