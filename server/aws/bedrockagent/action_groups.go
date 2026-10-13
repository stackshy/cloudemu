package bedrockagent

import (
	"encoding/json"
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

type actionGroupRequest struct {
	ActionGroupName                  string            `json:"actionGroupName"`
	Description                      string            `json:"description"`
	ClientToken                      string            `json:"clientToken"`
	ParentActionGroupSignature       string            `json:"parentActionGroupSignature"`
	ParentActionGroupSignatureParams map[string]string `json:"parentActionGroupSignatureParams"`
	ActionGroupState                 string            `json:"actionGroupState"`
	ActionGroupExecutor              json.RawMessage   `json:"actionGroupExecutor"`
	APISchema                        json.RawMessage   `json:"apiSchema"`
	FunctionSchema                   json.RawMessage   `json:"functionSchema"`
}

// actionGroupJSON is the AgentActionGroup shape. The model names the parent
// signature parentActionSignature in responses.
type actionGroupJSON struct {
	AgentID                          string            `json:"agentId"`
	AgentVersion                     string            `json:"agentVersion"`
	ActionGroupID                    string            `json:"actionGroupId"`
	ActionGroupName                  string            `json:"actionGroupName"`
	Description                      string            `json:"description,omitempty"`
	ClientToken                      string            `json:"clientToken,omitempty"`
	ParentActionSignature            string            `json:"parentActionSignature,omitempty"`
	ParentActionGroupSignatureParams map[string]string `json:"parentActionGroupSignatureParams,omitempty"`
	ActionGroupState                 string            `json:"actionGroupState"`
	ActionGroupExecutor              json.RawMessage   `json:"actionGroupExecutor,omitempty"`
	APISchema                        json.RawMessage   `json:"apiSchema,omitempty"`
	FunctionSchema                   json.RawMessage   `json:"functionSchema,omitempty"`
	CreatedAt                        string            `json:"createdAt"`
	UpdatedAt                        string            `json:"updatedAt"`
}

type actionGroupSummaryJSON struct {
	ActionGroupID    string `json:"actionGroupId"`
	ActionGroupName  string `json:"actionGroupName"`
	ActionGroupState string `json:"actionGroupState"`
	Description      string `json:"description,omitempty"`
	UpdatedAt        string `json:"updatedAt"`
}

type actionGroupEnvelope struct {
	AgentActionGroup actionGroupJSON `json:"agentActionGroup"`
}

type listActionGroupsResponse struct {
	ActionGroupSummaries []actionGroupSummaryJSON `json:"actionGroupSummaries"`
	NextToken            string                   `json:"nextToken,omitempty"`
}

// serveActionGroups routes /agents/{a}/agentversions/{v}/actiongroups/[{id}/].
//
//nolint:dupl // same route table as the other agent-version children, different resource.
func serveActionGroups(w http.ResponseWriter, r *http.Request, c badriver.ActionGroups, ref childRef) {
	ctx := r.Context()
	write := writeActionGroup(w)

	routeChild(w, r, ref, childOps{
		create: func() {
			if in, ok := decodeActionGroup(w, r); ok {
				write(c.CreateAgentActionGroup(ctx, in.config(ref)))
			}
		},
		list: func() { listActionGroups(w, r, c, ref) },
		get:  func() { write(c.GetAgentActionGroup(ctx, ref.agentID, ref.version, ref.childID)) },
		update: func() {
			if in, ok := decodeActionGroup(w, r); ok {
				write(c.UpdateAgentActionGroup(ctx, ref.childID, in.config(ref)))
			}
		},
		remove: func() { writeDeleted(w, c.DeleteAgentActionGroup(ctx, ref.agentID, ref.version, ref.childID)) },
	})
}

func decodeActionGroup(w http.ResponseWriter, r *http.Request) (*actionGroupRequest, bool) {
	var in actionGroupRequest

	return &in, decodeJSON(w, r, &in)
}

func listActionGroups(w http.ResponseWriter, r *http.Request, c badriver.ActionGroups, ref childRef) {
	page, ok := decodeListBody(w, r)
	if !ok {
		return
	}

	groups, next, err := c.ListAgentActionGroups(r.Context(), ref.agentID, ref.version, page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]actionGroupSummaryJSON, 0, len(groups))

	for i := range groups {
		g := &groups[i]
		out = append(out, actionGroupSummaryJSON{
			ActionGroupID: g.ID, ActionGroupName: g.Name, ActionGroupState: g.State,
			Description: g.Description, UpdatedAt: g.UpdatedAt,
		})
	}

	writeJSON(w, listActionGroupsResponse{ActionGroupSummaries: out, NextToken: next})
}

// writeActionGroup returns a writer for a (group, err) result.
func writeActionGroup(w http.ResponseWriter) func(*badriver.ActionGroup, error) {
	return func(g *badriver.ActionGroup, err error) {
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, actionGroupEnvelope{AgentActionGroup: actionGroupJSON{
			AgentID: g.AgentID, AgentVersion: g.AgentVersion, ActionGroupID: g.ID, ActionGroupName: g.Name,
			Description: g.Description, ClientToken: g.ClientToken, ParentActionSignature: g.ParentActionGroupSignature,
			ParentActionGroupSignatureParams: g.ParentActionGroupSignatureParams, ActionGroupState: g.State,
			ActionGroupExecutor: g.Executor, APISchema: g.APISchema, FunctionSchema: g.FunctionSchema,
			CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
		}})
	}
}

func (in *actionGroupRequest) config(ref childRef) badriver.ActionGroupConfig {
	return badriver.ActionGroupConfig{
		AgentID:                          ref.agentID,
		AgentVersion:                     ref.version,
		Name:                             in.ActionGroupName,
		Description:                      in.Description,
		ClientToken:                      in.ClientToken,
		ParentActionGroupSignature:       in.ParentActionGroupSignature,
		ParentActionGroupSignatureParams: in.ParentActionGroupSignatureParams,
		State:                            in.ActionGroupState,
		Executor:                         in.ActionGroupExecutor,
		APISchema:                        in.APISchema,
		FunctionSchema:                   in.FunctionSchema,
	}
}
