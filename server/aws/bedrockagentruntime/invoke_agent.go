package bedrockagentruntime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

// --- request shapes ---

type parameterJSON struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type responseBodyJSON map[string]struct {
	Body string `json:"body"`
}

type invocationResultJSON struct {
	FunctionResult *struct {
		ActionGroup       string           `json:"actionGroup"`
		Function          string           `json:"function"`
		ResponseBody      responseBodyJSON `json:"responseBody"`
		ResponseState     string           `json:"responseState"`
		ConfirmationState string           `json:"confirmationState"`
	} `json:"functionResult"`
	APIResult *struct {
		ActionGroup       string           `json:"actionGroup"`
		APIPath           string           `json:"apiPath"`
		HTTPMethod        string           `json:"httpMethod"`
		HTTPStatusCode    int32            `json:"httpStatusCode"`
		ResponseBody      responseBodyJSON `json:"responseBody"`
		ResponseState     string           `json:"responseState"`
		ConfirmationState string           `json:"confirmationState"`
	} `json:"apiResult"`
}

type sessionStateJSON struct {
	SessionAttributes              map[string]string      `json:"sessionAttributes"`
	PromptSessionAttributes        map[string]string      `json:"promptSessionAttributes"`
	InvocationID                   string                 `json:"invocationId"`
	ReturnControlInvocationResults []invocationResultJSON `json:"returnControlInvocationResults"`
	KnowledgeBaseConfigurations    []struct {
		KnowledgeBaseID        string                      `json:"knowledgeBaseId"`
		RetrievalConfiguration *retrievalConfigurationJSON `json:"retrievalConfiguration"`
	} `json:"knowledgeBaseConfigurations"`
	ConversationHistory *struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	} `json:"conversationHistory"`
}

type invokeAgentRequest struct {
	InputText               string            `json:"inputText"`
	EnableTrace             bool              `json:"enableTrace"`
	EndSession              bool              `json:"endSession"`
	MemoryID                string            `json:"memoryId"`
	SessionState            *sessionStateJSON `json:"sessionState"`
	StreamingConfigurations *streamingJSON    `json:"streamingConfigurations"`
}

// streamingJSON is StreamingConfigurations: streamFinalResponse streams the
// answer as several chunk events instead of one.
type streamingJSON struct {
	StreamFinalResponse bool `json:"streamFinalResponse"`
}

func (s *streamingJSON) streamFinal() bool { return s != nil && s.StreamFinalResponse }

func (s *sessionStateJSON) toDriver() *driver.SessionState {
	if s == nil {
		return nil
	}

	out := &driver.SessionState{
		SessionAttributes: s.SessionAttributes, PromptSessionAttributes: s.PromptSessionAttributes,
		InvocationID: s.InvocationID,
	}

	if h := s.ConversationHistory; h != nil {
		for _, msg := range h.Messages {
			var text strings.Builder
			for _, c := range msg.Content {
				text.WriteString(c.Text)
			}

			out.History = append(out.History, driver.HistoryMessage{Role: msg.Role, Text: text.String()})
		}
	}

	for _, kb := range s.KnowledgeBaseConfigurations {
		out.KnowledgeBases = append(out.KnowledgeBases, driver.KnowledgeBaseConfig{
			KnowledgeBaseID: kb.KnowledgeBaseID, NumberOfResults: kb.RetrievalConfiguration.numberOfResults(),
			Filter: kb.RetrievalConfiguration.filter(),
		})
	}

	for _, r := range s.ReturnControlInvocationResults {
		switch {
		case r.FunctionResult != nil:
			f := r.FunctionResult
			out.ReturnControlResults = append(out.ReturnControlResults, driver.InvocationResult{
				ActionGroup: f.ActionGroup, Function: f.Function, ResponseBody: firstBody(f.ResponseBody),
				ResponseState: f.ResponseState, ConfirmationState: f.ConfirmationState,
			})
		case r.APIResult != nil:
			a := r.APIResult
			out.ReturnControlResults = append(out.ReturnControlResults, driver.InvocationResult{
				ActionGroup: a.ActionGroup, APIPath: a.APIPath, HTTPMethod: a.HTTPMethod, HTTPStatusCode: a.HTTPStatusCode,
				ResponseBody: firstBody(a.ResponseBody), ResponseState: a.ResponseState, ConfirmationState: a.ConfirmationState,
			})
		}
	}

	return out
}

func firstBody(b responseBodyJSON) string {
	for _, k := range []string{"TEXT", "application/json"} {
		if v, ok := b[k]; ok {
			return v.Body
		}
	}

	for _, v := range b {
		return v.Body
	}

	return ""
}

// serveInvokeAgent handles InvokeAgent:
// POST /agents/{agentId}/agentAliases/{aliasId}/sessions/{sessionId}/text.
func serveInvokeAgent(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	var in invokeAgentRequest
	if !decodeJSONAllowEmpty(w, r, &in) {
		return
	}

	res, err := h.runtime.InvokeAgent(r.Context(), driver.InvokeAgentInput{
		AgentID: segs[1], AgentAliasID: segs[3], SessionID: segs[5], InputText: in.InputText,
		EnableTrace: in.EnableTrace, EndSession: in.EndSession, MemoryID: in.MemoryID, SessionState: in.SessionState.toDriver(),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeAgentStream(w, res, true, in.StreamingConfigurations.streamFinal())
}

// writeAgentStream streams an agent turn: trace and returnControl events, then
// the answer chunk, split into several when streamFinal is set (the citations
// ride on the last piece). InvokeInlineAgent traces carry no agent ids.
func writeAgentStream(w http.ResponseWriter, res *driver.InvokeAgentResult, withAgentIDs, streamFinal bool) {
	events := toAgentEvents(res)
	if len(events) == 0 {
		events = []streamEvent{{"chunk", chunkJSON{Bytes: []byte(res.Completion)}}}
	}

	if streamFinal {
		events = splitChunks(events)
	}

	ct := res.ContentType
	if ct == "" {
		ct = contentTypeJSON
	}

	for i := range events {
		if t, ok := events[i].payload.(tracePartJSON); ok && !withAgentIDs {
			t.AgentID, t.AgentAliasID, t.AgentVersion = "", "", ""
			events[i].payload = t
		}
	}

	writeEventStream(w, map[string]string{
		headerAgentSessionID: res.SessionID, headerAgentContentType: ct, headerAgentMemoryID: res.MemoryID,
	}, events)
}

// streamPieceBytes is about how much answer text one streamed chunk carries.
const streamPieceBytes = 64

// splitChunks replaces each chunk event with pieces of about streamPieceBytes,
// cut after a space so words stay whole.
func splitChunks(events []streamEvent) []streamEvent {
	out := make([]streamEvent, 0, len(events))

	for _, e := range events {
		c, ok := e.payload.(chunkJSON)
		if !ok || len(c.Bytes) <= streamPieceBytes {
			out = append(out, e)

			continue
		}

		rest := c.Bytes
		for len(rest) > streamPieceBytes {
			cut := bytes.IndexByte(rest[streamPieceBytes:], ' ')
			if cut < 0 {
				break
			}

			cut += streamPieceBytes + 1
			out = append(out, streamEvent{"chunk", chunkJSON{Bytes: rest[:cut]}})
			rest = rest[cut:]
		}

		out = append(out, streamEvent{"chunk", chunkJSON{Bytes: rest, Attribution: c.Attribution}})
	}

	return out
}

// --- response shapes ---

type chunkJSON struct {
	Bytes       []byte           `json:"bytes"`
	Attribution *attributionJSON `json:"attribution,omitempty"`
}

type attributionJSON struct {
	Citations []citationJSON `json:"citations"`
}

type tracePartJSON struct {
	AgentID      string                     `json:"agentId,omitempty"`
	AgentAliasID string                     `json:"agentAliasId,omitempty"`
	AgentVersion string                     `json:"agentVersion,omitempty"`
	SessionID    string                     `json:"sessionId"`
	EventTime    string                     `json:"eventTime,omitempty"`
	Trace        map[string]json.RawMessage `json:"trace"`
}

type returnControlJSON struct {
	InvocationID     string                `json:"invocationId"`
	InvocationInputs []invocationInputJSON `json:"invocationInputs"`
}

type invocationInputJSON struct {
	FunctionInvocationInput *functionInvocationJSON `json:"functionInvocationInput,omitempty"`
	APIInvocationInput      *apiInvocationJSON      `json:"apiInvocationInput,omitempty"`
}

type functionInvocationJSON struct {
	ActionGroup          string          `json:"actionGroup"`
	Function             string          `json:"function"`
	Parameters           []parameterJSON `json:"parameters"`
	ActionInvocationType string          `json:"actionInvocationType"`
}

type apiInvocationJSON struct {
	ActionGroup          string           `json:"actionGroup"`
	APIPath              string           `json:"apiPath"`
	HTTPMethod           string           `json:"httpMethod"`
	Parameters           []parameterJSON  `json:"parameters"`
	RequestBody          *requestBodyJSON `json:"requestBody,omitempty"`
	ActionInvocationType string           `json:"actionInvocationType"`
}

// requestBodyJSON is ApiRequestBody: content type -> properties.
type requestBodyJSON struct {
	Content map[string]propertiesJSON `json:"content"`
}

type propertiesJSON struct {
	Properties []parameterJSON `json:"properties"`
}

func toRequestBodyJSON(b *driver.RequestBody) *requestBodyJSON {
	if b == nil {
		return nil
	}

	props := make([]parameterJSON, 0, len(b.Properties))
	for _, p := range b.Properties {
		props = append(props, parameterJSON(p))
	}

	return &requestBodyJSON{Content: map[string]propertiesJSON{b.ContentType: {Properties: props}}}
}

func toAgentEvents(res *driver.InvokeAgentResult) []streamEvent {
	out := make([]streamEvent, 0, len(res.Events))

	for _, e := range res.Events {
		switch {
		case e.Chunk != nil:
			c := chunkJSON{Bytes: []byte(e.Chunk.Text)}
			if len(e.Chunk.Citations) > 0 {
				c.Attribution = &attributionJSON{Citations: toCitationsJSON(e.Chunk.Citations)}
			}

			out = append(out, streamEvent{"chunk", c})
		case e.Trace != nil:
			out = append(out, streamEvent{"trace", toTraceJSON(e.Trace)})
		case e.ReturnControl != nil:
			out = append(out, streamEvent{"returnControl", toReturnControlJSON(e.ReturnControl)})
		}
	}

	return out
}

func toReturnControlJSON(rc *driver.ReturnControl) returnControlJSON {
	out := returnControlJSON{InvocationID: rc.InvocationID, InvocationInputs: []invocationInputJSON{}}

	for _, in := range rc.Inputs {
		params := make([]parameterJSON, 0, len(in.Parameters))
		for _, p := range in.Parameters {
			params = append(params, parameterJSON(p))
		}

		if in.Function != "" {
			out.InvocationInputs = append(out.InvocationInputs, invocationInputJSON{FunctionInvocationInput: &functionInvocationJSON{
				ActionGroup: in.ActionGroup, Function: in.Function, Parameters: params, ActionInvocationType: "RESULT",
			}})

			continue
		}

		out.InvocationInputs = append(out.InvocationInputs, invocationInputJSON{APIInvocationInput: &apiInvocationJSON{
			ActionGroup: in.ActionGroup, APIPath: in.APIPath, HTTPMethod: in.HTTPMethod, Parameters: params,
			RequestBody: toRequestBodyJSON(in.RequestBody), ActionInvocationType: "RESULT",
		}})
	}

	return out
}

// toTraceJSON renders a trace as {"<kind>": {"<part>": {...}}}; a guardrail
// trace is {"guardrailTrace": {...}}.
func toTraceJSON(t *driver.Trace) tracePartJSON {
	var inner any

	switch t.Part {
	case "modelInvocationInput":
		inner = map[string]any{"traceId": t.TraceID, "text": t.Text, "type": t.PromptType, "foundationModel": t.FoundationModel}
	case "modelInvocationOutput":
		inner = modelOutputJSON(t)
	case "rationale":
		inner = map[string]any{"traceId": t.TraceID, "text": t.Text}
	case "invocationInput":
		inner = map[string]any{"traceId": t.TraceID, "invocationType": "ACTION_GROUP",
			"actionGroupInvocationInput": map[string]string{"actionGroupName": t.ActionGroup, "function": t.Function}}
	case "observation":
		inner = observationJSON(t)
	}

	var body []byte

	if t.Kind == "guardrailTrace" {
		body, _ = json.Marshal(map[string]string{"traceId": t.TraceID, "action": t.GuardrailAction})
	} else {
		body, _ = json.Marshal(map[string]any{t.Part: inner})
	}

	return tracePartJSON{
		AgentID: t.AgentID, AgentAliasID: t.AgentAliasID, AgentVersion: t.AgentVersion, SessionID: t.SessionID,
		EventTime: t.EventTime, Trace: map[string]json.RawMessage{t.Kind: body},
	}
}

// modelOutputJSON renders a model invocation output; pre- and post-processing
// steps also carry their parsed response.
func modelOutputJSON(t *driver.Trace) map[string]any {
	out := map[string]any{"traceId": t.TraceID, "rawResponse": map[string]string{"content": t.Text},
		"metadata": map[string]any{"usage": map[string]int32{"inputTokens": t.InputTokens, "outputTokens": t.OutputTokens}}}

	switch t.Kind {
	case "preProcessingTrace":
		out["parsedResponse"] = map[string]any{"isValid": true, "rationale": t.Text}
	case "postProcessingTrace":
		out["parsedResponse"] = map[string]string{"text": t.Text}
	}

	return out
}

func observationJSON(t *driver.Trace) map[string]any {
	out := map[string]any{"traceId": t.TraceID, "type": t.ObservationType}

	switch t.ObservationType {
	case "FINISH":
		out["finalResponse"] = map[string]string{"text": t.Text}
	case "ACTION_GROUP":
		out["actionGroupInvocationOutput"] = map[string]string{"text": t.Text}
	case "KNOWLEDGE_BASE":
		out["knowledgeBaseLookupOutput"] = map[string]any{"retrievedReferences": toReferencesJSON(t.References)}
	}

	return out
}
