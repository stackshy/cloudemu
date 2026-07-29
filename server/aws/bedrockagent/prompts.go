package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// servePrompts dispatches the /prompts subtree.
func (h *Handler) servePrompts(w http.ResponseWriter, r *http.Request, segs []string) {
	switch {
	case len(segs) == 0:
		h.servePromptCollection(w, r)
	case len(segs) == 1:
		h.servePromptItem(w, r, segs[0])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) servePromptCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createPrompt(w, r)
	case http.MethodGet:
		h.listPrompts(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) servePromptItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.getPrompt(w, r, id)
	case http.MethodPut:
		h.updatePrompt(w, r, id)
	case http.MethodDelete:
		h.deletePrompt(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// --- operations ---

func (h *Handler) createPrompt(w http.ResponseWriter, r *http.Request) {
	var in createPromptRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	prompt, err := h.agent.CreatePrompt(r.Context(), badriver.PromptConfig{
		Name:                     in.Name,
		Description:              in.Description,
		DefaultVariant:           in.DefaultVariant,
		CustomerEncryptionKeyArn: in.CustomerEncryptionKeyArn,
		Variants:                 in.Variants,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toPromptJSON(prompt))
}

func (h *Handler) getPrompt(w http.ResponseWriter, r *http.Request, id string) {
	prompt, err := h.agent.GetPrompt(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toPromptJSON(prompt))
}

func (h *Handler) listPrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := h.agent.ListPrompts(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]promptSummaryJSON, 0, len(prompts))
	for i := range prompts {
		out = append(out, toPromptSummaryJSON(&prompts[i]))
	}

	writeJSON(w, listPromptsResponse{PromptSummaries: out})
}

//nolint:dupl // structurally similar to updateFlow but operates on a distinct resource type.
func (h *Handler) updatePrompt(w http.ResponseWriter, r *http.Request, id string) {
	var in createPromptRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	prompt, err := h.agent.UpdatePrompt(r.Context(), id, badriver.PromptConfig{
		Name:                     in.Name,
		Description:              in.Description,
		DefaultVariant:           in.DefaultVariant,
		CustomerEncryptionKeyArn: in.CustomerEncryptionKeyArn,
		Variants:                 in.Variants,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toPromptJSON(prompt))
}

func (h *Handler) deletePrompt(w http.ResponseWriter, r *http.Request, id string) {
	pid, err := h.agent.DeletePrompt(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deletePromptResponse{ID: pid})
}

// --- converters ---

func toPromptJSON(p *badriver.Prompt) promptJSON {
	return promptJSON{
		Arn:                      p.ARN,
		ID:                       p.ID,
		Name:                     p.Name,
		Version:                  p.Version,
		Description:              p.Description,
		DefaultVariant:           p.DefaultVariant,
		CustomerEncryptionKeyArn: p.CustomerEncryptionKeyArn,
		Variants:                 p.Variants,
		CreatedAt:                p.CreatedAt,
		UpdatedAt:                p.UpdatedAt,
	}
}

func toPromptSummaryJSON(p *badriver.Prompt) promptSummaryJSON {
	return promptSummaryJSON{
		Arn:         p.ARN,
		ID:          p.ID,
		Name:        p.Name,
		Version:     p.Version,
		Description: p.Description,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}
