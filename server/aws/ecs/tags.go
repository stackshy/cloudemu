package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string    `json:"resourceArn"`
		Tags        []wireTag `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.ecs.TagResource(r.Context(), req.ResourceArn, toTags(req.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.ecs.UntagResource(r.Context(), req.ResourceArn, req.TagKeys); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.ecs.ListTagsForResource(r.Context(), req.ResourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"tags": fromTags(tags)})
}
