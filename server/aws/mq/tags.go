package mq

import (
	"net/http"
	"strings"
)

// serveTags routes /v1/tags/{resourceArn}. The MQ ARN is a single path segment
// (it contains no slash), but it is rejoined defensively.
//
// CreateTags is POST /v1/tags/{arn}, DeleteTags is
// DELETE /v1/tags/{arn}?tagKeys=..., ListTags is GET.
func (h *Handler) serveTags(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	resourceArn := strings.Join(rest, "/")

	switch r.Method {
	case http.MethodPost:
		h.createTags(w, r, resourceArn)
	case http.MethodDelete:
		h.deleteTags(w, r, resourceArn)
	case http.MethodGet:
		h.listTags(w, r, resourceArn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createTags(w http.ResponseWriter, r *http.Request, resourceArn string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	if err := h.mq.CreateTags(r.Context(), resourceArn, tagsFromBody(raw)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) deleteTags(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tagKeys := r.URL.Query()["tagKeys"]

	if err := h.mq.DeleteTags(r.Context(), resourceArn, tagKeys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, resourceArn string) {
	tags, err := h.mq.ListTags(r.Context(), resourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	if tags == nil {
		tags = map[string]string{}
	}

	writeJSON(w, map[string]any{"tags": tags})
}
