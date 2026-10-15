package bedrockagent

import (
	"context"
	"net/http"
	"strings"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// servePrompts dispatches the /prompts subtree.
func (h *Handler) servePrompts(w http.ResponseWriter, r *http.Request, segs []string) {
	switch {
	case len(segs) == 0:
		h.servePromptCollection(w, r)
	case len(segs) == 1:
		h.servePromptItem(w, r, segs[0])
	case len(segs) == promptVersionsSegments && segs[1] == segVersions && r.Method == http.MethodPost:
		withCapability(h, w, "CreatePromptVersion", func(c badriver.PromptVersions) { createPromptVersion(w, r, c, segs[0]) })
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
	version := r.URL.Query().Get("promptVersion")

	if suffix := promptSuffixVersion(id); suffix != "" && r.Method == http.MethodDelete {
		if version != "" && version != suffix {
			writeError(w, http.StatusBadRequest, "ValidationException",
				"promptIdentifier names version "+suffix+" but promptVersion is "+version)

			return
		}

		// A version-qualified identifier deletes only that version.
		version = suffix
	}

	if version != "" && (r.Method == http.MethodGet || r.Method == http.MethodDelete) {
		withCapability(h, w, "PromptVersions", func(c badriver.PromptVersions) { servePromptVersion(w, r, c, id, version) })

		return
	}

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

//nolint:dupl // structurally similar to createFlow but operates on a distinct resource type.
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
		Tags:                     in.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSONStatus(w, http.StatusCreated, toPromptJSON(prompt))
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
	page, ok := queryPage(w, r)
	if !ok {
		return
	}

	list := h.agent.ListPrompts

	if id := r.URL.Query().Get("promptIdentifier"); id != "" {
		c, ok := capabilityOf[badriver.PromptVersions](h.agent, w, "ListPrompts")
		if !ok {
			return
		}

		list = func(ctx context.Context, page badriver.Page) ([]badriver.Prompt, string, error) {
			return c.ListPromptVersions(ctx, id, page)
		}
	}

	prompts, next, err := list(r.Context(), page)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]promptSummaryJSON, 0, len(prompts))
	for i := range prompts {
		out = append(out, toPromptSummaryJSON(&prompts[i]))
	}

	writeJSON(w, listPromptsResponse{PromptSummaries: out, NextToken: next})
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

// promptSuffixVersion returns the version a prompt/ID:N identifier names, or
// "" for an unqualified identifier.
func promptSuffixVersion(identifier string) string {
	_, version, _ := strings.Cut(resourceIDOf(identifier, "prompt/"), ":")

	return version
}
