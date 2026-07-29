package bedrockagentruntime

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	bedrockagentruntimedriver "github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

func (h *Handler) invokeAgent(w http.ResponseWriter, r *http.Request, agentID, agentAliasID, sessionID string) {
	var in invokeAgentRequest
	if !decodeJSONAllowEmpty(w, r, &in) {
		return
	}

	res, err := h.runtime.InvokeAgent(r.Context(), bedrockagentruntimedriver.InvokeAgentInput{
		AgentID:      agentID,
		AgentAliasID: agentAliasID,
		SessionID:    sessionID,
		InputText:    in.InputText,
		EnableTrace:  in.EnableTrace,
		EndSession:   in.EndSession,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeInvokeAgentStream(w, res)
}

func (h *Handler) retrieve(w http.ResponseWriter, r *http.Request, knowledgeBaseID string) {
	var in retrieveRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	res, err := h.runtime.Retrieve(r.Context(), bedrockagentruntimedriver.RetrieveInput{
		KnowledgeBaseID: knowledgeBaseID,
		QueryText:       in.RetrievalQuery.Text,
		NextToken:       in.NextToken,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toRetrieveResponse(res))
}

func (h *Handler) retrieveAndGenerate(w http.ResponseWriter, r *http.Request) {
	var in retrieveAndGenerateRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	res, err := h.runtime.RetrieveAndGenerate(r.Context(), bedrockagentruntimedriver.RetrieveAndGenerateInput{
		InputText: in.Input.Text,
		SessionID: in.SessionID,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, retrieveAndGenerateResponse{
		Output:    retrieveAndGenerateOutputBody{Text: res.Text},
		SessionID: res.SessionID,
		Citations: []any{},
	})
}

// --- converters ---

func toRetrieveResponse(res *bedrockagentruntimedriver.RetrieveResult) retrieveResponse {
	out := make([]knowledgeBaseRetrievalResult, 0, len(res.Results))

	for i := range res.Results {
		r := &res.Results[i]
		item := knowledgeBaseRetrievalResult{
			Content: retrievalResultContent{Type: "TEXT", Text: r.Text},
			Score:   r.Score,
		}

		if r.LocationURI != "" {
			item.Location = &retrievalResultLocation{
				Type:       "S3",
				S3Location: &retrievalResultS3Location{URI: r.LocationURI},
			}
		}

		out = append(out, item)
	}

	return retrieveResponse{RetrievalResults: out, NextToken: res.NextToken}
}

// --- helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

// decodeJSONAllowEmpty decodes a JSON body but tolerates an empty body, since
// InvokeAgent's body members are all optional.
func decodeJSONAllowEmpty(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
