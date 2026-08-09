package opensearch

import "net/http"

// serveTags handles POST /2021-01-01/tags (AddTags) and
// GET /2021-01-01/tags?arn= (ListTags).
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.addTags(w, r)
	case http.MethodGet:
		h.listTags(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveTagsRemoval handles POST /2021-01-01/tags-removal (RemoveTags).
func (h *Handler) serveTagsRemoval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	var req struct {
		ARN     string   `json:"ARN"`
		TagKeys []string `json:"TagKeys"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.os.RemoveTags(r.Context(), req.ARN, req.TagKeys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) addTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ARN     string `json:"ARN"`
		TagList []tag  `json:"TagList"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.os.AddTags(r.Context(), req.ARN, tagsToMap(req.TagList)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.os.ListTags(r.Context(), r.URL.Query().Get("arn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"TagList": mapToTags(tags)})
}
