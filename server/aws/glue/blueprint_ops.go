package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type blueprintJSON struct {
	Name              string   `json:"Name"`
	Description       string   `json:"Description,omitempty"`
	CreatedOn         *float64 `json:"CreatedOn,omitempty"`
	LastModifiedOn    *float64 `json:"LastModifiedOn,omitempty"`
	ParameterSpec     string   `json:"ParameterSpec,omitempty"`
	BlueprintLocation string   `json:"BlueprintLocation,omitempty"`
	Status            string   `json:"Status,omitempty"`
}

func blueprintToWire(b *driver.Blueprint) blueprintJSON {
	return blueprintJSON{
		Name: b.Name, Description: b.Description, CreatedOn: epochOrNil(b.CreatedOn),
		LastModifiedOn: epochOrNil(b.LastModifiedOn), ParameterSpec: b.ParameterSpec,
		BlueprintLocation: b.BlueprintLocation, Status: b.Status,
	}
}

type createBlueprintRequest struct {
	Name              string `json:"Name"`
	Description       string `json:"Description"`
	BlueprintLocation string `json:"BlueprintLocation"`
}

func (h *Handler) createBlueprint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createBlueprintRequest) (any, error) {
		name, err := h.glue.CreateBlueprint(ctx, driver.Blueprint{
			Name: req.Name, Description: req.Description, BlueprintLocation: req.BlueprintLocation,
		})
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type blueprintNameRequest struct {
	Name string `json:"Name"`
}

type getBlueprintResponse struct {
	Blueprint blueprintJSON `json:"Blueprint"`
}

func (h *Handler) getBlueprint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *blueprintNameRequest) (any, error) {
		b, err := h.glue.GetBlueprint(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getBlueprintResponse{Blueprint: blueprintToWire(b)}, nil
	})
}

func (h *Handler) updateBlueprint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createBlueprintRequest) (any, error) {
		name, err := h.glue.UpdateBlueprint(ctx, req.Name, driver.Blueprint{
			Name: req.Name, Description: req.Description, BlueprintLocation: req.BlueprintLocation,
		})
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

func (h *Handler) deleteBlueprint(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *blueprintNameRequest) (any, error) {
		name, err := h.glue.DeleteBlueprint(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type listBlueprintsResponse struct {
	Blueprints []string `json:"Blueprints"`
	NextToken  string   `json:"NextToken,omitempty"`
}

func (h *Handler) listBlueprints(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		names, next, err := h.glue.ListBlueprints(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		return listBlueprintsResponse{Blueprints: names, NextToken: next}, nil
	})
}

type batchGetBlueprintsRequest struct {
	Names []string `json:"Names"`
}

type batchGetBlueprintsResponse struct {
	Blueprints        []blueprintJSON `json:"Blueprints"`
	MissingBlueprints []string        `json:"MissingBlueprints,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) batchGetBlueprints(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetBlueprintsRequest) (any, error) {
		found, missing, err := h.glue.BatchGetBlueprints(ctx, req.Names)
		if err != nil {
			return nil, err
		}

		out := make([]blueprintJSON, 0, len(found))
		for i := range found {
			out = append(out, blueprintToWire(&found[i]))
		}

		return batchGetBlueprintsResponse{Blueprints: out, MissingBlueprints: missing}, nil
	})
}

type startBlueprintRunRequest struct {
	BlueprintName string `json:"BlueprintName"`
	Parameters    string `json:"Parameters"`
	RoleArn       string `json:"RoleArn"`
}

type startBlueprintRunResponse struct {
	RunID string `json:"RunId"`
}

func (h *Handler) startBlueprintRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startBlueprintRunRequest) (any, error) {
		id, err := h.glue.StartBlueprintRun(ctx, req.BlueprintName, req.RoleArn, req.Parameters)
		if err != nil {
			return nil, err
		}

		return startBlueprintRunResponse{RunID: id}, nil
	})
}

type blueprintRunJSON struct {
	RunID         string   `json:"RunID,omitempty"`
	BlueprintName string   `json:"BlueprintName,omitempty"`
	WorkflowName  string   `json:"WorkflowName,omitempty"`
	State         string   `json:"State,omitempty"`
	StartedOn     *float64 `json:"StartedOn,omitempty"`
	CompletedOn   *float64 `json:"CompletedOn,omitempty"`
	Parameters    string   `json:"Parameters,omitempty"`
	RoleArn       string   `json:"RoleArn,omitempty"`
}

func blueprintRunToWire(br *driver.BlueprintRun) blueprintRunJSON {
	return blueprintRunJSON{
		RunID: br.RunID, BlueprintName: br.BlueprintName, WorkflowName: br.WorkflowName, State: br.State,
		StartedOn: epochOrNil(br.StartedOn), CompletedOn: epochOrNil(br.CompletedOn),
		Parameters: br.Parameters, RoleArn: br.RoleARN,
	}
}

type getBlueprintRunRequest struct {
	BlueprintName string `json:"BlueprintName"`
	RunID         string `json:"RunId"`
}

type getBlueprintRunResponse struct {
	BlueprintRun blueprintRunJSON `json:"BlueprintRun"`
}

func (h *Handler) getBlueprintRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getBlueprintRunRequest) (any, error) {
		br, err := h.glue.GetBlueprintRun(ctx, req.BlueprintName, req.RunID)
		if err != nil {
			return nil, err
		}

		return getBlueprintRunResponse{BlueprintRun: blueprintRunToWire(br)}, nil
	})
}

type getBlueprintRunsRequest struct {
	BlueprintName string `json:"BlueprintName"`
	NextToken     string `json:"NextToken"`
	MaxResults    int32  `json:"MaxResults"`
}

type getBlueprintRunsResponse struct {
	BlueprintRuns []blueprintRunJSON `json:"BlueprintRuns"`
	NextToken     string             `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getBlueprintRuns(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getBlueprintRunsRequest) (any, error) {
		runs, next, err := h.glue.GetBlueprintRuns(ctx, req.BlueprintName,
			driver.TablePagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]blueprintRunJSON, 0, len(runs))
		for i := range runs {
			out = append(out, blueprintRunToWire(&runs[i]))
		}

		return getBlueprintRunsResponse{BlueprintRuns: out, NextToken: next}, nil
	})
}
