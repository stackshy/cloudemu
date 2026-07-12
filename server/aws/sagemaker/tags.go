package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) addTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string    `json:"ResourceArn"`
		Tags        []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.svc.AddTags(r.Context(), req.ResourceArn, toTags(req.Tags))
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Tags": fromTags(tags)})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.svc.ListTags(r.Context(), req.ResourceArn)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Tags": fromTags(tags)})
}

func (h *Handler) deleteTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.DeleteTags(r.Context(), req.ResourceArn, req.TagKeys); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}
