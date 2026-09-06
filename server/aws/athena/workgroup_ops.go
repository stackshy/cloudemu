package athena

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

type createWorkGroupRequest struct {
	Name          string               `json:"Name"`
	Configuration *workGroupConfigJSON `json:"Configuration"`
	Description   string               `json:"Description"`
	Tags          []tagJSON            `json:"Tags"`
}

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func (h *Handler) createWorkGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createWorkGroupRequest) (any, error) {
		wg := driver.WorkGroup{
			Name:          req.Name,
			Description:   req.Description,
			Configuration: workGroupConfigFromWire(req.Configuration),
			Tags:          tagsToMap(req.Tags),
		}
		if err := h.athena.CreateWorkGroup(ctx, wg); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type getWorkGroupRequest struct {
	WorkGroup string `json:"WorkGroup"`
}

type getWorkGroupResponse struct {
	WorkGroup workGroupJSON `json:"WorkGroup"`
}

func (h *Handler) getWorkGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getWorkGroupRequest) (any, error) {
		wg, err := h.athena.GetWorkGroup(ctx, req.WorkGroup)
		if err != nil {
			return nil, err
		}

		return getWorkGroupResponse{WorkGroup: workGroupToWire(wg)}, nil
	})
}

type updateWorkGroupRequest struct {
	WorkGroup            string                      `json:"WorkGroup"`
	Description          *string                     `json:"Description"`
	State                *string                     `json:"State"`
	ConfigurationUpdates *workGroupConfigUpdatesJSON `json:"ConfigurationUpdates"`
}

func (h *Handler) updateWorkGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateWorkGroupRequest) (any, error) {
		upd := driver.WorkGroupUpdate{
			Description:          req.Description,
			State:                req.State,
			ConfigurationUpdates: configUpdatesFromWire(req.ConfigurationUpdates),
		}
		if err := h.athena.UpdateWorkGroup(ctx, req.WorkGroup, upd); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type deleteWorkGroupRequest struct {
	WorkGroup             string `json:"WorkGroup"`
	RecursiveDeleteOption bool   `json:"RecursiveDeleteOption"`
}

func (h *Handler) deleteWorkGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteWorkGroupRequest) (any, error) {
		if err := h.athena.DeleteWorkGroup(ctx, req.WorkGroup, req.RecursiveDeleteOption); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listWorkGroupsRequest struct {
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listWorkGroupsResponse struct {
	WorkGroups []workGroupSummaryJSON `json:"WorkGroups"`
	NextToken  string                 `json:"NextToken,omitempty"`
}

func (h *Handler) listWorkGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listWorkGroupsRequest) (any, error) {
		wgs, next, err := h.athena.ListWorkGroups(ctx,
			driver.Pagination{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		out := make([]workGroupSummaryJSON, 0, len(wgs))
		for _, s := range wgs {
			out = append(out, workGroupSummaryToWire(s))
		}

		return listWorkGroupsResponse{WorkGroups: out, NextToken: next}, nil
	})
}
