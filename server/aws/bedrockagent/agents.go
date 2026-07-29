package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// serveAgents dispatches the /agents/ subtree.
func (h *Handler) serveAgents(w http.ResponseWriter, r *http.Request, segs []string) {
	switch {
	case len(segs) == 0:
		h.serveAgentCollection(w, r)
	case len(segs) == 1:
		h.serveAgentItem(w, r, segs[0])
	case len(segs) == 2 && segs[1] == segAgentAliases:
		h.serveAgentAlias(w, r, segs[0])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveAgentCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		h.createAgent(w, r)
	case http.MethodPost:
		h.listAgents(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveAgentItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getAgent(w, r, id)
	case http.MethodPut:
		h.updateAgent(w, r, id)
	case http.MethodDelete:
		h.deleteAgent(w, r, id)
	case http.MethodPost:
		h.prepareAgent(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveAgentAlias(w http.ResponseWriter, r *http.Request, agentID string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	h.createAgentAlias(w, r, agentID)
}

// --- operations ---

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var in createAgentRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	agent, err := h.agent.CreateAgent(r.Context(), badriver.AgentConfig{
		Name:                    in.AgentName,
		ResourceRoleArn:         in.AgentResourceRoleArn,
		FoundationModel:         in.FoundationModel,
		Instruction:             in.Instruction,
		Description:             in.Description,
		IdleSessionTTLInSeconds: in.IdleSessionTTLInSeconds,
		ClientToken:             in.ClientToken,
		Tags:                    in.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, agentEnvelope{Agent: toAgentJSON(agent)})
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request, id string) {
	agent, err := h.agent.GetAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, agentEnvelope{Agent: toAgentJSON(agent)})
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.agent.ListAgents(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]agentSummaryJSON, 0, len(agents))
	for i := range agents {
		out = append(out, toAgentSummaryJSON(&agents[i]))
	}

	writeJSON(w, listAgentsResponse{AgentSummaries: out})
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	var in createAgentRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	agent, err := h.agent.UpdateAgent(r.Context(), id, badriver.AgentConfig{
		Name:                    in.AgentName,
		ResourceRoleArn:         in.AgentResourceRoleArn,
		FoundationModel:         in.FoundationModel,
		Instruction:             in.Instruction,
		Description:             in.Description,
		IdleSessionTTLInSeconds: in.IdleSessionTTLInSeconds,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, agentEnvelope{Agent: toAgentJSON(agent)})
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request, id string) {
	status, err := h.agent.DeleteAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deleteAgentResponse{AgentID: id, AgentStatus: status})
}

func (h *Handler) prepareAgent(w http.ResponseWriter, r *http.Request, id string) {
	agent, err := h.agent.PrepareAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, prepareAgentResponse{
		AgentID:      agent.ID,
		AgentStatus:  agent.Status,
		AgentVersion: agent.Version,
		PreparedAt:   agent.PreparedAt,
	})
}

func (h *Handler) createAgentAlias(w http.ResponseWriter, r *http.Request, agentID string) {
	var in createAgentAliasRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	alias, err := h.agent.CreateAgentAlias(r.Context(), badriver.AgentAliasConfig{
		AgentID:     agentID,
		Name:        in.AgentAliasName,
		Description: in.Description,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, agentAliasEnvelope{AgentAlias: toAgentAliasJSON(alias)})
}

// --- converters ---

func toAgentJSON(a *badriver.Agent) agentJSON {
	return agentJSON{
		AgentID:                 a.ID,
		AgentARN:                a.ARN,
		AgentName:               a.Name,
		AgentResourceRoleArn:    a.ResourceRoleArn,
		FoundationModel:         a.FoundationModel,
		Instruction:             a.Instruction,
		Description:             a.Description,
		AgentStatus:             a.Status,
		AgentVersion:            a.Version,
		IdleSessionTTLInSeconds: a.IdleSessionTTLInSeconds,
		CreatedAt:               a.CreatedAt,
		UpdatedAt:               a.UpdatedAt,
		PreparedAt:              a.PreparedAt,
	}
}

func toAgentSummaryJSON(a *badriver.Agent) agentSummaryJSON {
	return agentSummaryJSON{
		AgentID:     a.ID,
		AgentName:   a.Name,
		AgentStatus: a.Status,
		Description: a.Description,
		UpdatedAt:   a.UpdatedAt,
	}
}

func toAgentAliasJSON(a *badriver.AgentAlias) agentAliasJSON {
	return agentAliasJSON{
		AgentAliasID:         a.ID,
		AgentAliasARN:        a.ARN,
		AgentAliasName:       a.Name,
		AgentID:              a.AgentID,
		AgentAliasStatus:     a.Status,
		Description:          a.Description,
		RoutingConfiguration: []string{},
		CreatedAt:            a.CreatedAt,
		UpdatedAt:            a.UpdatedAt,
	}
}
