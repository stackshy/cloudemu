package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// serveFlows dispatches the /flows subtree.
func (h *Handler) serveFlows(w http.ResponseWriter, r *http.Request, segs []string) {
	switch {
	case len(segs) == 0:
		h.serveFlowCollection(w, r)
	case len(segs) == 1:
		h.serveFlowItem(w, r, segs[0])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveFlowCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFlow(w, r)
	case http.MethodGet:
		h.listFlows(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveFlowItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getFlow(w, r, id)
	case http.MethodPut:
		h.updateFlow(w, r, id)
	case http.MethodDelete:
		h.deleteFlow(w, r, id)
	case http.MethodPost:
		h.prepareFlow(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// --- operations ---

func (h *Handler) createFlow(w http.ResponseWriter, r *http.Request) {
	var in createFlowRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	flow, err := h.agent.CreateFlow(r.Context(), badriver.FlowConfig{
		Name:                     in.Name,
		ExecutionRoleArn:         in.ExecutionRoleArn,
		Description:              in.Description,
		CustomerEncryptionKeyArn: in.CustomerEncryptionKeyArn,
		Definition:               in.Definition,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toFlowJSON(flow))
}

func (h *Handler) getFlow(w http.ResponseWriter, r *http.Request, id string) {
	flow, err := h.agent.GetFlow(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toFlowJSON(flow))
}

func (h *Handler) listFlows(w http.ResponseWriter, r *http.Request) {
	flows, err := h.agent.ListFlows(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]flowSummaryJSON, 0, len(flows))
	for i := range flows {
		out = append(out, toFlowSummaryJSON(&flows[i]))
	}

	writeJSON(w, listFlowsResponse{FlowSummaries: out})
}

//nolint:dupl // structurally similar to updatePrompt but operates on a distinct resource type.
func (h *Handler) updateFlow(w http.ResponseWriter, r *http.Request, id string) {
	var in createFlowRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	flow, err := h.agent.UpdateFlow(r.Context(), id, badriver.FlowConfig{
		Name:                     in.Name,
		ExecutionRoleArn:         in.ExecutionRoleArn,
		Description:              in.Description,
		CustomerEncryptionKeyArn: in.CustomerEncryptionKeyArn,
		Definition:               in.Definition,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toFlowJSON(flow))
}

func (h *Handler) deleteFlow(w http.ResponseWriter, r *http.Request, id string) {
	fid, err := h.agent.DeleteFlow(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deleteFlowResponse{ID: fid})
}

func (h *Handler) prepareFlow(w http.ResponseWriter, r *http.Request, id string) {
	flow, err := h.agent.PrepareFlow(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, prepareFlowResponse{ID: flow.ID, Status: flow.Status})
}

// --- converters ---

func toFlowJSON(f *badriver.Flow) flowJSON {
	return flowJSON{
		Arn:                      f.ARN,
		ID:                       f.ID,
		Name:                     f.Name,
		Status:                   f.Status,
		Version:                  f.Version,
		ExecutionRoleArn:         f.ExecutionRoleArn,
		Description:              f.Description,
		CustomerEncryptionKeyArn: f.CustomerEncryptionKeyArn,
		Definition:               f.Definition,
		CreatedAt:                f.CreatedAt,
		UpdatedAt:                f.UpdatedAt,
	}
}

func toFlowSummaryJSON(f *badriver.Flow) flowSummaryJSON {
	return flowSummaryJSON{
		Arn:         f.ARN,
		ID:          f.ID,
		Name:        f.Name,
		Status:      f.Status,
		Version:     f.Version,
		Description: f.Description,
		CreatedAt:   f.CreatedAt,
		UpdatedAt:   f.UpdatedAt,
	}
}
