package fis

import (
	"net/http"
	"strings"
)

// serveTags handles the shared /tags/{resourceArn} operations: POST
// (TagResource), GET (ListTagsForResource) and DELETE (UntagResource). The ARN
// is the whole path below /tags/; net/http has already percent-decoded it, so
// its colons and the slash inside the resource part arrive as separate path
// segments that rest rejoins.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	arn := strings.Join(rest, "/")
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
		Tags map[string]string `json:"tags"`
	}

	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.fis.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	keys := r.URL.Query()["tagKeys"]

	if err := h.fis.UntagResource(r.Context(), arn, keys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.fis.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	body := map[string]any{}
	if len(tags) > 0 {
		body["tags"] = tags
	}

	writeJSON(w, body)
}
