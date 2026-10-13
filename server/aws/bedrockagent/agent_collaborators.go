package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

type agentDescriptorJSON struct {
	AliasArn string `json:"aliasArn,omitempty"`
}

type collaboratorRequest struct {
	AgentDescriptor          agentDescriptorJSON `json:"agentDescriptor"`
	CollaboratorName         string              `json:"collaboratorName"`
	CollaborationInstruction string              `json:"collaborationInstruction"`
	RelayConversationHistory string              `json:"relayConversationHistory"`
	ClientToken              string              `json:"clientToken"`
}

type collaboratorJSON struct {
	AgentID                  string              `json:"agentId"`
	AgentVersion             string              `json:"agentVersion"`
	CollaboratorID           string              `json:"collaboratorId"`
	AgentDescriptor          agentDescriptorJSON `json:"agentDescriptor"`
	CollaboratorName         string              `json:"collaboratorName"`
	CollaborationInstruction string              `json:"collaborationInstruction"`
	RelayConversationHistory string              `json:"relayConversationHistory"`
	ClientToken              string              `json:"clientToken,omitempty"`
	CreatedAt                string              `json:"createdAt"`
	LastUpdatedAt            string              `json:"lastUpdatedAt"`
}

type collaboratorEnvelope struct {
	AgentCollaborator collaboratorJSON `json:"agentCollaborator"`
}

type listCollaboratorsResponse struct {
	AgentCollaboratorSummaries []collaboratorJSON `json:"agentCollaboratorSummaries"`
	NextToken                  string             `json:"nextToken,omitempty"`
}

// serveCollaborators routes
// /agents/{a}/agentversions/{v}/agentcollaborators/[{id}/].
//
//nolint:dupl // same route table as the other agent-version children, different resource.
func serveCollaborators(w http.ResponseWriter, r *http.Request, c badriver.AgentCollaborators, ref childRef) {
	ctx := r.Context()
	write := writeCollaborator(w)

	routeChild(w, r, ref, childOps{
		create: func() {
			if in, ok := decodeCollaborator(w, r); ok {
				write(c.AssociateAgentCollaborator(ctx, in.config(ref)))
			}
		},
		list: func() { listCollaborators(w, r, c, ref) },
		get:  func() { write(c.GetAgentCollaborator(ctx, ref.agentID, ref.version, ref.childID)) },
		update: func() {
			if in, ok := decodeCollaborator(w, r); ok {
				write(c.UpdateAgentCollaborator(ctx, ref.childID, in.config(ref)))
			}
		},
		remove: func() { writeDeleted(w, c.DisassociateAgentCollaborator(ctx, ref.agentID, ref.version, ref.childID)) },
	})
}

func decodeCollaborator(w http.ResponseWriter, r *http.Request) (*collaboratorRequest, bool) {
	var in collaboratorRequest

	return &in, decodeJSON(w, r, &in)
}

func listCollaborators(w http.ResponseWriter, r *http.Request, c badriver.AgentCollaborators, ref childRef) {
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	items, next, err := c.ListAgentCollaborators(r.Context(), ref.agentID, ref.version, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]collaboratorJSON, 0, len(items))

	for i := range items {
		item := toCollaboratorJSON(&items[i])
		item.ClientToken = ""
		out = append(out, item)
	}

	writeJSON(w, listCollaboratorsResponse{AgentCollaboratorSummaries: out, NextToken: next})
}

func writeCollaborator(w http.ResponseWriter) func(*badriver.AgentCollaborator, error) {
	return func(col *badriver.AgentCollaborator, err error) {
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, collaboratorEnvelope{AgentCollaborator: toCollaboratorJSON(col)})
	}
}

func toCollaboratorJSON(c *badriver.AgentCollaborator) collaboratorJSON {
	return collaboratorJSON{
		AgentID: c.AgentID, AgentVersion: c.AgentVersion, CollaboratorID: c.ID,
		AgentDescriptor: agentDescriptorJSON{AliasArn: c.AliasARN}, CollaboratorName: c.Name,
		CollaborationInstruction: c.Instruction, RelayConversationHistory: c.RelayConversationHistory,
		ClientToken: c.ClientToken, CreatedAt: c.CreatedAt, LastUpdatedAt: c.LastUpdatedAt,
	}
}

func (in *collaboratorRequest) config(ref childRef) badriver.AgentCollaboratorConfig {
	return badriver.AgentCollaboratorConfig{
		AgentID:                  ref.agentID,
		AgentVersion:             ref.version,
		AliasARN:                 in.AgentDescriptor.AliasArn,
		Name:                     in.CollaboratorName,
		Instruction:              in.CollaborationInstruction,
		RelayConversationHistory: in.RelayConversationHistory,
		ClientToken:              in.ClientToken,
	}
}
