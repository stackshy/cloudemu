package bedrock

import (
	"net/http"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// countTokensBody wraps the countTokensRequest union under the "input" key the
// real bedrockruntime SDK emits ({"input":{"converse"|"invokeModel":...}}).
type countTokensBody struct {
	Input countTokensRequest `json:"input"`
}

// countTokens handles POST /model/{modelId}/count-tokens. The request body is a
// union carrying either a converse or an invokeModel member.
func (h *Handler) countTokens(w http.ResponseWriter, r *http.Request, modelID string) {
	var body countTokensBody
	if !decodeJSON(w, r, &body) {
		return
	}

	req := body.Input
	in := bedrockdriver.CountTokensInput{ModelID: modelID}

	switch {
	case req.InvokeModel != nil:
		in.InvokeBody = req.InvokeModel.Body
	case req.Converse != nil:
		in.Messages = toDriverMessages(req.Converse.Messages)
		in.System = textsOf(req.Converse.System)
	}

	n, err := h.bedrock.CountTokens(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, countTokensResponse{InputTokens: n})
}

// applyGuardrail handles POST
// /guardrail/{guardrailIdentifier}/version/{guardrailVersion}/apply. The
// identifier and version arrive as path parameters.
func (h *Handler) applyGuardrail(w http.ResponseWriter, r *http.Request, guardrailID, guardrailVersion string) {
	var req applyGuardrailRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	contents := make([]string, 0, len(req.Content))

	for _, block := range req.Content {
		if block.Text != nil {
			contents = append(contents, block.Text.Text)
		}
	}

	out, err := h.bedrock.ApplyGuardrail(r.Context(), bedrockdriver.ApplyGuardrailInput{
		GuardrailIdentifier: guardrailID,
		GuardrailVersion:    guardrailVersion,
		Source:              req.Source,
		Content:             contents,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	outputs := make([]applyGuardrailOutputContent, 0, len(out.Outputs))
	for _, o := range out.Outputs {
		outputs = append(outputs, applyGuardrailOutputContent{Text: o})
	}

	writeJSON(w, applyGuardrailResponse{
		Usage:       applyGuardrailUsage{},
		Action:      out.Action,
		Outputs:     outputs,
		Assessments: []applyGuardrailAssessment{},
	})
}

// toDriverMessages maps converse wire messages to driver messages.
func toDriverMessages(msgs []converseMessage) []bedrockdriver.Message {
	out := make([]bedrockdriver.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, bedrockdriver.Message{Role: m.Role, Text: textsOf(m.Content)})
	}

	return out
}
