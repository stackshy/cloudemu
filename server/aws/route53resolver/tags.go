package route53resolver

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string    `json:"ResourceArn"`
		Tags        []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.r53r.TagResource(r.Context(), req.ResourceArn, toDriverTags(req.Tags)); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.r53r.UntagResource(r.Context(), req.ResourceArn, req.TagKeys); err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := h.r53r.ListTagsForResource(r.Context(), req.ResourceArn)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"Tags": tagsToWire(tags)})
}
