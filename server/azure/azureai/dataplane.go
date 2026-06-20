package azureai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	csdriver "github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/errors"
)

const (
	maxDPBody  = 4 << 20
	scorePath  = "/score"
	openaiRoot = "/openai/"
)

// DataPlaneHandler serves the Azure OpenAI inference + Assistants data plane and
// AML online-endpoint scoring. Routing is path-based; the account scope is
// derived from the request Host subdomain (e.g. {account}.openai.azure.com).
type DataPlaneHandler struct {
	dp csdriver.DataPlane
}

// NewDataPlane returns a data-plane handler backed by drv.
func NewDataPlane(drv csdriver.DataPlane) *DataPlaneHandler {
	return &DataPlaneHandler{dp: drv}
}

// Matches claims the /openai/ data-plane surface and the /score AML scoring
// endpoint.
func (*DataPlaneHandler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return strings.HasPrefix(p, openaiRoot) || p == scorePath
}

// ServeHTTP routes by path.
func (h *DataPlaneHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == scorePath {
		h.score(w, r)

		return
	}

	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/openai"), "/"), "/")
	account := accountFromHost(r.Host)

	switch parts[0] {
	case collDeployments:
		h.serveInference(w, r, account, parts)
	case "assistants":
		h.serveAssistants(w, r, account, parts)
	case "threads":
		h.serveThreads(w, r, account, parts)
	default:
		dpErr(w, http.StatusNotFound, "unknown data-plane resource: "+parts[0])
	}
}

// accountFromHost extracts the account label from an Azure AI data-plane host
// such as {account}.openai.azure.com. Falls back to "default" for hosts that
// don't carry an account subdomain (e.g. a bare httptest 127.0.0.1).
func accountFromHost(host string) string {
	host, _, _ = strings.Cut(host, ":")
	for _, infix := range []string{".openai.", ".services.ai.", ".cognitiveservices."} {
		if i := strings.Index(host, infix); i > 0 {
			return host[:i]
		}
	}

	return "default"
}

func (h *DataPlaneHandler) serveInference(w http.ResponseWriter, r *http.Request, account string, parts []string) {
	// /openai/deployments/{deployment}/{action}
	const minSegs = 3
	if len(parts) < minSegs || r.Method != http.MethodPost {
		dpErr(w, http.StatusNotFound, "unsupported inference path")

		return
	}

	deployment, action := parts[1], parts[2]

	switch action {
	case "chat": // .../chat/completions
		h.chatCompletions(w, r, account, deployment)
	case "completions":
		h.completions(w, r, account, deployment)
	case "embeddings":
		h.embeddings(w, r, account, deployment)
	default:
		dpErr(w, http.StatusNotFound, "unknown inference action: "+action)
	}
}

func (h *DataPlaneHandler) chatCompletions(w http.ResponseWriter, r *http.Request, account, deployment string) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature *float64 `json:"temperature"`
		MaxTokens   *int     `json:"max_tokens"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	req := csdriver.ChatCompletionRequest{Temperature: body.Temperature, MaxTokens: body.MaxTokens}
	for _, msg := range body.Messages {
		req.Messages = append(req.Messages, csdriver.ChatMessage{Role: msg.Role, Content: msg.Content})
	}

	resp, err := h.dp.ChatCompletions(r.Context(), account, deployment, req)
	if err != nil {
		dpCErr(w, err)

		return
	}

	choices := make([]map[string]any, 0, len(resp.Choices))
	for _, c := range resp.Choices {
		choices = append(choices, map[string]any{
			"index":         c.Index,
			"message":       map[string]any{"role": c.Message.Role, "content": c.Message.Content},
			"finish_reason": c.FinishReason,
		})
	}

	dpJSON(w, map[string]any{
		"id": resp.ID, "object": "chat.completion", "model": resp.Model, "created": resp.Created,
		"choices": choices, "usage": usageJSON(resp.Usage),
	})
}

func (h *DataPlaneHandler) completions(w http.ResponseWriter, r *http.Request, account, deployment string) {
	var body struct {
		Prompt    string `json:"prompt"`
		MaxTokens *int   `json:"max_tokens"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	resp, err := h.dp.Completions(r.Context(), account, deployment,
		csdriver.CompletionsRequest{Prompt: body.Prompt, MaxTokens: body.MaxTokens})
	if err != nil {
		dpCErr(w, err)

		return
	}

	choices := make([]map[string]any, 0, len(resp.Choices))
	for _, c := range resp.Choices {
		choices = append(choices, map[string]any{"text": c.Text, "index": c.Index, "finish_reason": c.FinishReason})
	}

	dpJSON(w, map[string]any{
		"id": resp.ID, "object": "text_completion", "model": resp.Model, "created": resp.Created,
		"choices": choices, "usage": usageJSON(resp.Usage),
	})
}

func (h *DataPlaneHandler) embeddings(w http.ResponseWriter, r *http.Request, account, deployment string) {
	var body struct {
		Input json.RawMessage `json:"input"`
	}

	if !dpDecode(w, r, &body) {
		return
	}

	resp, err := h.dp.Embeddings(r.Context(), account, deployment,
		csdriver.EmbeddingsRequest{Input: parseEmbeddingInput(body.Input)})
	if err != nil {
		dpCErr(w, err)

		return
	}

	data := make([]map[string]any, 0, len(resp.Data))
	for _, d := range resp.Data {
		data = append(data, map[string]any{"object": "embedding", "index": d.Index, "embedding": d.Embedding})
	}

	dpJSON(w, map[string]any{
		"object": "list", "model": resp.Model, "data": data, "usage": usageJSON(resp.Usage),
	})
}

// parseEmbeddingInput accepts either a JSON string or an array of strings.
func parseEmbeddingInput(raw json.RawMessage) []string {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}
	}

	var many []string
	_ = json.Unmarshal(raw, &many)

	return many
}

func usageJSON(u csdriver.TokenUsage) map[string]any {
	return map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens, "total_tokens": u.TotalTokens,
	}
}

func (h *DataPlaneHandler) score(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		dpErr(w, http.StatusMethodNotAllowed, "method not allowed")

		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDPBody))
	if err != nil {
		dpErr(w, http.StatusBadRequest, "failed to read body")

		return
	}

	out, cerr := h.dp.ScoreOnlineEndpoint(r.Context(), accountFromHost(r.Host), body)
	if cerr != nil {
		dpCErr(w, cerr)

		return
	}

	// Re-encode through json (breaks request->response taint; keeps valid JSON).
	var payload any
	if json.Unmarshal(out, &payload) != nil {
		payload = map[string]any{"raw": string(out)}
	}

	dpJSON(w, payload)
}

// --- wire helpers ---

func dpDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDPBody))
	if err != nil {
		dpErr(w, http.StatusBadRequest, "failed to read body")

		return false
	}

	if err := json.Unmarshal(body, v); err != nil {
		dpErr(w, http.StatusBadRequest, "invalid JSON body")

		return false
	}

	return true
}

func dpJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func dpErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "code": status}})
}

// dpCErr maps a typed cloud error to the OpenAI-style error envelope.
func dpCErr(w http.ResponseWriter, err error) {
	switch {
	case errors.IsNotFound(err):
		dpErr(w, http.StatusNotFound, err.Error())
	case errors.IsInvalidArgument(err):
		dpErr(w, http.StatusBadRequest, err.Error())
	case errors.IsAlreadyExists(err), errors.IsFailedPrecondition(err):
		dpErr(w, http.StatusConflict, err.Error())
	default:
		dpErr(w, http.StatusInternalServerError, err.Error())
	}
}
