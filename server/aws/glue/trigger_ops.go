package glue

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

type triggerJSON struct {
	Name         string           `json:"Name"`
	WorkflowName string           `json:"WorkflowName,omitempty"`
	Type         string           `json:"Type,omitempty"`
	State        string           `json:"State,omitempty"`
	Schedule     string           `json:"Schedule,omitempty"`
	Description  string           `json:"Description,omitempty"`
	Actions      []map[string]any `json:"Actions,omitempty"`
	Predicate    map[string]any   `json:"Predicate,omitempty"`
}

func triggerToWire(t *driver.Trigger) triggerJSON {
	return triggerJSON{
		Name: t.Name, WorkflowName: t.WorkflowName, Type: t.Type, State: t.State,
		Schedule: t.Schedule, Description: t.Description, Actions: t.Actions, Predicate: t.Predicate,
	}
}

type createTriggerRequest struct {
	Name         string           `json:"Name"`
	WorkflowName string           `json:"WorkflowName"`
	Type         string           `json:"Type"`
	Schedule     string           `json:"Schedule"`
	Description  string           `json:"Description"`
	Actions      []map[string]any `json:"Actions"`
	Predicate    map[string]any   `json:"Predicate"`
}

func (h *Handler) createTrigger(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createTriggerRequest) (any, error) {
		name, err := h.glue.CreateTrigger(ctx, driver.Trigger{
			Name: req.Name, WorkflowName: req.WorkflowName, Type: req.Type, Schedule: req.Schedule,
			Description: req.Description, Actions: req.Actions, Predicate: req.Predicate,
		})
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type triggerNameRequest struct {
	Name string `json:"Name"`
}

type getTriggerResponse struct {
	Trigger triggerJSON `json:"Trigger"`
}

func (h *Handler) getTrigger(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *triggerNameRequest) (any, error) {
		t, err := h.glue.GetTrigger(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return getTriggerResponse{Trigger: triggerToWire(t)}, nil
	})
}

type updateTriggerRequest struct {
	Name          string `json:"Name"`
	TriggerUpdate struct {
		Description string           `json:"Description"`
		Schedule    string           `json:"Schedule"`
		Actions     []map[string]any `json:"Actions"`
		Predicate   map[string]any   `json:"Predicate"`
	} `json:"TriggerUpdate"`
}

func (h *Handler) updateTrigger(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateTriggerRequest) (any, error) {
		t, err := h.glue.UpdateTrigger(ctx, req.Name, driver.Trigger{
			Name: req.Name, Description: req.TriggerUpdate.Description, Schedule: req.TriggerUpdate.Schedule,
			Actions: req.TriggerUpdate.Actions, Predicate: req.TriggerUpdate.Predicate,
		})
		if err != nil {
			return nil, err
		}

		return getTriggerResponse{Trigger: triggerToWire(t)}, nil
	})
}

type triggerNameResponse struct {
	Name string `json:"Name"`
}

func (h *Handler) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *triggerNameRequest) (any, error) {
		name, err := h.glue.DeleteTrigger(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return triggerNameResponse{Name: name}, nil
	})
}

type getTriggersResponse struct {
	Triggers  []triggerJSON `json:"Triggers"`
	NextToken string        `json:"NextToken,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) getTriggers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		ts, next, err := h.glue.GetTriggers(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		out := make([]triggerJSON, 0, len(ts))
		for i := range ts {
			out = append(out, triggerToWire(&ts[i]))
		}

		return getTriggersResponse{Triggers: out, NextToken: next}, nil
	})
}

type listTriggersResponse struct {
	TriggerNames []string `json:"TriggerNames"`
	NextToken    string   `json:"NextToken,omitempty"`
}

func (h *Handler) listTriggers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *paginationRequest) (any, error) {
		names, next, err := h.glue.ListTriggers(ctx, pageOf(*req))
		if err != nil {
			return nil, err
		}

		return listTriggersResponse{TriggerNames: names, NextToken: next}, nil
	})
}

func (h *Handler) startTrigger(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *triggerNameRequest) (any, error) {
		name, err := h.glue.StartTrigger(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

func (h *Handler) stopTrigger(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *triggerNameRequest) (any, error) {
		name, err := h.glue.StopTrigger(ctx, req.Name)
		if err != nil {
			return nil, err
		}

		return nameResponse{Name: name}, nil
	})
}

type batchGetTriggersRequest struct {
	TriggerNames []string `json:"TriggerNames"`
}

type batchGetTriggersResponse struct {
	Triggers         []triggerJSON `json:"Triggers"`
	TriggersNotFound []string      `json:"TriggersNotFound,omitempty"`
}

//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (h *Handler) batchGetTriggers(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *batchGetTriggersRequest) (any, error) {
		found, notFound, err := h.glue.BatchGetTriggers(ctx, req.TriggerNames)
		if err != nil {
			return nil, err
		}

		out := make([]triggerJSON, 0, len(found))
		for i := range found {
			out = append(out, triggerToWire(&found[i]))
		}

		return batchGetTriggersResponse{Triggers: out, TriggersNotFound: notFound}, nil
	})
}
