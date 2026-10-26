package codeartifact

import (
	"net/http"
)

// tagRequest is the TagResource body.
type tagRequest struct {
	Tags []tagPair `json:"tags"`
}

// untagRequest is the UntagResource body.
type untagRequest struct {
	TagKeys []string `json:"tagKeys"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.ca.TagResource(r.Context(), r.URL.Query().Get("resourceArn"), tagsToMap(req.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req untagRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if err := h.ca.UntagResource(r.Context(), r.URL.Query().Get("resourceArn"), req.TagKeys); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	tags, err := h.ca.ListTagsForResource(r.Context(), r.URL.Query().Get("resourceArn"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"tags": tagsToWire(tags)})
}
