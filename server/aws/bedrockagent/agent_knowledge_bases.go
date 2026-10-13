package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

type agentKBRequest struct {
	KnowledgeBaseID    string `json:"knowledgeBaseId"`
	Description        string `json:"description"`
	KnowledgeBaseState string `json:"knowledgeBaseState"`
}

type agentKBJSON struct {
	AgentID            string `json:"agentId"`
	AgentVersion       string `json:"agentVersion"`
	KnowledgeBaseID    string `json:"knowledgeBaseId"`
	Description        string `json:"description"`
	KnowledgeBaseState string `json:"knowledgeBaseState"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

type agentKBSummaryJSON struct {
	KnowledgeBaseID    string `json:"knowledgeBaseId"`
	Description        string `json:"description,omitempty"`
	KnowledgeBaseState string `json:"knowledgeBaseState"`
	UpdatedAt          string `json:"updatedAt"`
}

type agentKBEnvelope struct {
	AgentKnowledgeBase agentKBJSON `json:"agentKnowledgeBase"`
}

type listAgentKBsResponse struct {
	AgentKnowledgeBaseSummaries []agentKBSummaryJSON `json:"agentKnowledgeBaseSummaries"`
	NextToken                   string               `json:"nextToken,omitempty"`
}

// serveAgentKBs routes /agents/{a}/agentversions/{v}/knowledgebases/[{kb}/].
func serveAgentKBs(w http.ResponseWriter, r *http.Request, c badriver.AgentKnowledgeBases, ref childRef) {
	ctx := r.Context()
	write := writeAgentKB(w)

	routeChild(w, r, ref, childOps{
		create: func() {
			if in, ok := decodeAgentKB(w, r); ok {
				write(c.AssociateAgentKnowledgeBase(ctx, in.config(ref, in.KnowledgeBaseID)))
			}
		},
		list: func() { listAgentKBs(w, r, c, ref) },
		get:  func() { write(c.GetAgentKnowledgeBase(ctx, ref.agentID, ref.version, ref.childID)) },
		update: func() {
			if in, ok := decodeAgentKB(w, r); ok {
				write(c.UpdateAgentKnowledgeBase(ctx, in.config(ref, ref.childID)))
			}
		},
		remove: func() { writeDeleted(w, c.DisassociateAgentKnowledgeBase(ctx, ref.agentID, ref.version, ref.childID)) },
	})
}

func decodeAgentKB(w http.ResponseWriter, r *http.Request) (*agentKBRequest, bool) {
	var in agentKBRequest

	return &in, decodeJSON(w, r, &in)
}

func listAgentKBs(w http.ResponseWriter, r *http.Request, c badriver.AgentKnowledgeBases, ref childRef) {
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	items, next, err := c.ListAgentKnowledgeBases(r.Context(), ref.agentID, ref.version, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]agentKBSummaryJSON, 0, len(items))

	for i := range items {
		a := &items[i]
		out = append(out, agentKBSummaryJSON{
			KnowledgeBaseID: a.KnowledgeBaseID, Description: a.Description,
			KnowledgeBaseState: a.State, UpdatedAt: a.UpdatedAt,
		})
	}

	writeJSON(w, listAgentKBsResponse{AgentKnowledgeBaseSummaries: out, NextToken: next})
}

func writeAgentKB(w http.ResponseWriter) func(*badriver.AgentKnowledgeBase, error) {
	return func(a *badriver.AgentKnowledgeBase, err error) {
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, agentKBEnvelope{AgentKnowledgeBase: agentKBJSON{
			AgentID: a.AgentID, AgentVersion: a.AgentVersion, KnowledgeBaseID: a.KnowledgeBaseID,
			Description: a.Description, KnowledgeBaseState: a.State, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		}})
	}
}

func (in *agentKBRequest) config(ref childRef, kbID string) badriver.AgentKnowledgeBaseConfig {
	return badriver.AgentKnowledgeBaseConfig{
		AgentID:         ref.agentID,
		AgentVersion:    ref.version,
		KnowledgeBaseID: kbID,
		Description:     in.Description,
		State:           in.KnowledgeBaseState,
	}
}
