package location

import (
	"net/http"
	"strings"
)

// serveTags handles the shared /tags/{ResourceArn} operations: POST
// (TagResource), GET (ListTagsForResource) and DELETE (UntagResource). The ARN
// is the whole path below /tags/; net/http has already percent-decoded it, so
// its colons and slashes arrive intact.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request) {
	arn := strings.TrimPrefix(r.URL.Path, tagsPrefix)
	if arn == "" {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch r.Method {
	case http.MethodPost:
		h.tagResource(w, r, arn)
	case http.MethodGet:
		h.listTagsForResource(w, r, arn)
	case http.MethodDelete:
		h.untagResource(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request, arn string) {
	var req struct {
		Tags map[string]string `json:"Tags"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.loc.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	keys := r.URL.Query()["tagKeys"]

	if err := h.loc.UntagResource(r.Context(), arn, keys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.loc.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{}
	if len(tags) > 0 {
		body["Tags"] = tags
	}

	writeJSON(w, body)
}
