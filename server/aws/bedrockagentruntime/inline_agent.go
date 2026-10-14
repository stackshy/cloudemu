package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type inlineActionGroupJSON struct {
	ActionGroupName            string `json:"actionGroupName"`
	Description                string `json:"description"`
	ParentActionGroupSignature string `json:"parentActionGroupSignature"`
	ActionGroupExecutor        *struct {
		Lambda        string `json:"lambda"`
		CustomControl string `json:"customControl"`
	} `json:"actionGroupExecutor"`
	FunctionSchema *struct {
		Functions []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Parameters  map[string]struct {
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"parameters"`
		} `json:"functions"`
	} `json:"functionSchema"`
	APISchema json.RawMessage `json:"apiSchema"`
}

type inlineKnowledgeBaseJSON struct {
	KnowledgeBaseID        string                      `json:"knowledgeBaseId"`
	Description            string                      `json:"description"`
	RetrievalConfiguration *retrievalConfigurationJSON `json:"retrievalConfiguration"`
}

type guardrailConfigJSON struct {
	GuardrailIdentifier string `json:"guardrailIdentifier"`
	GuardrailVersion    string `json:"guardrailVersion"`
}

type inlineAgentRequest struct {
	AgentName               string                    `json:"agentName"`
	FoundationModel         string                    `json:"foundationModel"`
	Instruction             string                    `json:"instruction"`
	InputText               string                    `json:"inputText"`
	EnableTrace             bool                      `json:"enableTrace"`
	EndSession              bool                      `json:"endSession"`
	IdleSessionTTLInSeconds int32                     `json:"idleSessionTTLInSeconds"`
	AgentCollaboration      string                    `json:"agentCollaboration"`
	ActionGroups            []inlineActionGroupJSON   `json:"actionGroups"`
	KnowledgeBases          []inlineKnowledgeBaseJSON `json:"knowledgeBases"`
	GuardrailConfiguration  *guardrailConfigJSON      `json:"guardrailConfiguration"`
	InlineSessionState      *sessionStateJSON         `json:"inlineSessionState"`
	StreamingConfigurations *streamingJSON            `json:"streamingConfigurations"`
}

// serveInlineAgent handles InvokeInlineAgent: POST /agents/{sessionId}.
func serveInlineAgent(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "InvokeInlineAgent", func(c driver.InlineAgents) {
		var in inlineAgentRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		res, err := c.InvokeInlineAgent(r.Context(), in.toDriver(segs[1]))
		if err != nil {
			writeErr(w, err)

			return
		}

		writeAgentStream(w, res, false, in.StreamingConfigurations.streamFinal())
	})
}

func (in *inlineAgentRequest) toDriver(sessionID string) driver.InlineAgentInput {
	out := driver.InlineAgentInput{
		SessionID: sessionID, AgentName: in.AgentName, FoundationModel: in.FoundationModel, Instruction: in.Instruction,
		InputText: in.InputText, EnableTrace: in.EnableTrace, EndSession: in.EndSession,
		IdleSessionTTLInSeconds: in.IdleSessionTTLInSeconds, AgentCollaboration: in.AgentCollaboration,
		SessionState: in.InlineSessionState.toDriver(),
	}

	for _, g := range in.ActionGroups {
		out.ActionGroups = append(out.ActionGroups, g.toDriver())
	}

	for _, kb := range in.KnowledgeBases {
		ref := driver.InlineKnowledgeBase{
			KnowledgeBaseID: kb.KnowledgeBaseID, Description: kb.Description, Filter: kb.RetrievalConfiguration.filter(),
		}
		if kb.RetrievalConfiguration != nil && kb.RetrievalConfiguration.VectorSearchConfiguration != nil {
			ref.NumberOfResults = kb.RetrievalConfiguration.VectorSearchConfiguration.NumberOfResults
		}

		out.KnowledgeBases = append(out.KnowledgeBases, ref)
	}

	out.Guardrail = in.GuardrailConfiguration.toDriver()

	return out
}

func (g *inlineActionGroupJSON) toDriver() driver.InlineActionGroup {
	out := driver.InlineActionGroup{
		Name: g.ActionGroupName, Description: g.Description, ParentActionGroupSignature: g.ParentActionGroupSignature,
		APISchema: g.APISchema,
	}

	if g.ActionGroupExecutor != nil {
		out.ReturnControl = g.ActionGroupExecutor.CustomControl == "RETURN_CONTROL"
		out.LambdaARN = g.ActionGroupExecutor.Lambda
	}

	if g.FunctionSchema != nil {
		for _, f := range g.FunctionSchema.Functions {
			fd := driver.FunctionDef{Name: f.Name, Description: f.Description}

			for _, name := range sortedKeys(f.Parameters) {
				p := f.Parameters[name]
				fd.Parameters = append(fd.Parameters, driver.ParameterDef{Name: name, Type: p.Type, Required: p.Required})
			}

			out.Functions = append(out.Functions, fd)
		}
	}

	return out
}

func (g *guardrailConfigJSON) toDriver() *driver.GuardrailRef {
	if g == nil || g.GuardrailIdentifier == "" {
		return nil
	}

	return &driver.GuardrailRef{ID: g.GuardrailIdentifier, Version: g.GuardrailVersion}
}
