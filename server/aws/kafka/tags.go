package kafka

import (
	"net/http"
)

// routeTags handles /v1/tags/{resourceArn}: POST (tag), GET (list), DELETE
// (untag). The resource ARN is the sole path segment.
func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 1 {
		notFoundPath(w, r.URL.Path)

		return
	}

	arn := rest[0]

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

	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	if err := h.k.TagResource(r.Context(), arn, req.Tags); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request, arn string) {
	if err := h.k.UntagResource(r.Context(), arn, r.URL.Query()["tagKeys"]); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request, arn string) {
	tags, err := h.k.ListTagsForResource(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"tags": tags})
}
