package kinesisvideo

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// tagMapFromList folds a Tag list (the resource-level tagging body shape) into
// the string-to-string map the driver takes.
func tagMapFromList(list []driver.Tag) map[string]string {
	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}

	return out
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string       `json:"ResourceARN"`
		Tags        []driver.Tag `json:"Tags"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.kv.TagResource(r.Context(), req.ResourceARN, tagMapFromList(req.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string   `json:"ResourceARN"`
		TagKeyList  []string `json:"TagKeyList"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.kv.UntagResource(r.Context(), req.ResourceARN, req.TagKeyList); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceARN string `json:"ResourceARN"`
		NextToken   string `json:"NextToken"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	tags, next, err := h.kv.ListTagsForResource(r.Context(), req.ResourceARN, req.NextToken)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{"Tags": tags}
	if next != "" {
		body["NextToken"] = next
	}

	writeJSON(w, body)
}
