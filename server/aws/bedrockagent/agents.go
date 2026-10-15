package bedrockagent

import (
	"encoding/json"
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
	case segs[1] == segAgentAliases:
		h.serveAgentAliases(w, r, segs)
	case segs[1] == segAgentVersions:
		h.serveAgentVersions(w, r, segs)
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

// --- operations ---

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var in createAgentRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	agent, err := h.agent.CreateAgent(r.Context(), in.config())
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusAccepted, agentEnvelope{Agent: toAgentJSON(agent)})
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
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	agents, next, err := h.agent.ListAgents(r.Context(), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]agentSummaryJSON, 0, len(agents))
	for i := range agents {
		out = append(out, toAgentSummaryJSON(&agents[i]))
	}

	writeJSON(w, listAgentsResponse{AgentSummaries: out, NextToken: next})
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	var in createAgentRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	cfg := in.config()
	cfg.ClientToken, cfg.Tags = "", nil

	agent, err := h.agent.UpdateAgent(r.Context(), id, cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusAccepted, agentEnvelope{Agent: toAgentJSON(agent)})
}

// deleteAgent handles DeleteAgent. A backend that can check whether the agent
// is in use honors skipResourceInUseCheck; any other deletes outright.
func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request, id string) {
	var (
		status string
		err    error
	)

	if c, ok := h.agent.(badriver.AgentInUseDeleter); ok {
		status, err = c.DeleteAgentChecked(r.Context(), id, queryBool(r, "skipResourceInUseCheck"))
	} else {
		status, err = h.agent.DeleteAgent(r.Context(), id)
	}

	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusAccepted, deleteAgentResponse{AgentID: id, AgentStatus: status})
}

func (h *Handler) prepareAgent(w http.ResponseWriter, r *http.Request, id string) {
	agent, err := h.agent.PrepareAgent(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusAccepted, prepareAgentResponse{
		AgentID:      agent.ID,
		AgentStatus:  agent.Status,
		AgentVersion: agent.Version,
		PreparedAt:   agent.PreparedAt,
	})
}

// --- converters ---

// emptyPromptOverride is the promptOverrideConfiguration reported for an agent
// created without one. Terraform dereferences it on every read.
const emptyPromptOverride = `{"promptConfigurations":[]}`

func toAgentJSON(a *badriver.Agent) agentJSON {
	override := a.PromptOverrideConfiguration
	if len(override) == 0 {
		override = json.RawMessage(emptyPromptOverride)
	}

	return agentJSON{
		AgentID:                     a.ID,
		AgentARN:                    a.ARN,
		AgentName:                   a.Name,
		AgentResourceRoleArn:        a.ResourceRoleArn,
		FoundationModel:             a.FoundationModel,
		Instruction:                 a.Instruction,
		Description:                 a.Description,
		AgentStatus:                 a.Status,
		AgentVersion:                a.Version,
		IdleSessionTTLInSeconds:     a.IdleSessionTTLInSeconds,
		CreatedAt:                   a.CreatedAt,
		UpdatedAt:                   a.UpdatedAt,
		PreparedAt:                  a.PreparedAt,
		AgentCollaboration:          a.AgentCollaboration,
		OrchestrationType:           a.OrchestrationType,
		CustomerEncryptionKeyArn:    a.CustomerEncryptionKeyArn,
		PromptOverrideConfiguration: override,
		GuardrailConfiguration:      a.GuardrailConfiguration,
		MemoryConfiguration:         a.MemoryConfiguration,
		CustomOrchestration:         a.CustomOrchestration,
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
