package vertexai

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// wireContent is the JSON shape of a generateContent content turn.
type wireContent struct {
	Role  string `json:"role"`
	Parts []struct {
		Text string `json:"text"`
	} `json:"parts"`
}

type wireGenerateRequest struct {
	Contents          []wireContent `json:"contents"`
	SystemInstruction *wireContent  `json:"systemInstruction"`
	GenerationConfig  *struct {
		Temperature     *float64 `json:"temperature"`
		TopP            *float64 `json:"topP"`
		TopK            *int     `json:"topK"`
		MaxOutputTokens *int     `json:"maxOutputTokens"`
	} `json:"generationConfig"`
}

func toContents(in []wireContent) []driver.Content {
	out := make([]driver.Content, 0, len(in))

	for _, c := range in {
		parts := make([]driver.Part, 0, len(c.Parts))
		for _, p := range c.Parts {
			parts = append(parts, driver.Part{Text: p.Text})
		}

		out = append(out, driver.Content{Role: c.Role, Parts: parts})
	}

	return out
}

func toDriverRequest(req wireGenerateRequest) driver.GenerateContentRequest {
	dr := driver.GenerateContentRequest{Contents: toContents(req.Contents)}

	if req.SystemInstruction != nil {
		si := toContents([]wireContent{*req.SystemInstruction})
		dr.SystemInstruction = &si[0]
	}

	if req.GenerationConfig != nil {
		dr.GenerationConfig = &driver.GenerationConfig{
			Temperature:     req.GenerationConfig.Temperature,
			TopP:            req.GenerationConfig.TopP,
			TopK:            req.GenerationConfig.TopK,
			MaxOutputTokens: req.GenerationConfig.MaxOutputTokens,
		}
	}

	return dr
}

func generateResponseJSON(resp *driver.GenerateContentResponse) map[string]any {
	cands := make([]map[string]any, 0, len(resp.Candidates))

	for _, c := range resp.Candidates {
		parts := make([]map[string]any, 0, len(c.Content.Parts))
		for _, p := range c.Content.Parts {
			parts = append(parts, map[string]any{"text": p.Text})
		}

		cands = append(cands, map[string]any{
			"content":      map[string]any{"role": c.Content.Role, "parts": parts},
			"finishReason": c.FinishReason,
		})
	}

	return map[string]any{
		"candidates": cands,
		"usageMetadata": map[string]any{
			"promptTokenCount":     resp.UsageMetadata.PromptTokenCount,
			"candidatesTokenCount": resp.UsageMetadata.CandidatesTokenCount,
			"totalTokenCount":      resp.UsageMetadata.TotalTokenCount,
		},
	}
}

// servePublishers handles /v1/publishers/{pub}/models/{model}:{action}.
func (h *Handler) servePublishers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/v1/")

	model, action := splitActionPair(rest)
	if action == "" {
		writeError(w, http.StatusNotFound, "notFound", "missing action on publishers path")

		return
	}

	h.runGenAI(w, r, model, action)
}

func (h *Handler) endpointGenerateContent(w http.ResponseWriter, r *http.Request, endpoint string) {
	h.runGenAI(w, r, endpoint, "generateContent")
}

// runGenAI dispatches generateContent / countTokens for either a publisher
// model path or an endpoint resource name.
func (h *Handler) runGenAI(w http.ResponseWriter, r *http.Request, model, action string) {
	var req wireGenerateRequest
	if !decode(w, r, &req) {
		return
	}

	switch action {
	case "generateContent":
		resp, err := h.svc.GenerateContent(r.Context(), model, toDriverRequest(req))
		if err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, generateResponseJSON(resp))
	case actionStreamGenerateContent:
		resp, err := h.svc.GenerateContent(r.Context(), model, toDriverRequest(req))
		if err != nil {
			writeCErr(w, err)

			return
		}

		// streamGenerateContent returns a JSON array of response chunks; emit a
		// single-element array so SDK stream decoders iterate it correctly
		// (a lone object fails array-decoding / yields zero chunks).
		writeJSON(w, []map[string]any{generateResponseJSON(resp)})
	case "countTokens":
		resp, err := h.svc.CountTokens(r.Context(), model, toDriverRequest(req))
		if err != nil {
			writeCErr(w, err)

			return
		}

		writeJSON(w, map[string]any{"totalTokens": resp.TotalTokens})
	default:
		writeError(w, http.StatusNotFound, "notFound", "unknown generative action: "+action)
	}
}
