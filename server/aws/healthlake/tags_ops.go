package healthlake

import (
	"context"
	"net/http"
)

// registerTagRoutes wires the resource-tagging operations.
func (h *Handler) registerTagRoutes() {
	h.routes["TagResource"] = h.tagResource
	h.routes["UntagResource"] = h.untagResource
	h.routes["ListTagsForResource"] = h.listTagsForResource
}

type tagResourceRequest struct {
	ResourceARN string    `json:"ResourceARN"`
	Tags        []tagJSON `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.healthlake.TagResource(ctx, req.ResourceARN, tagsFromWire(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceRequest struct {
	ResourceARN string   `json:"ResourceARN"`
	TagKeys     []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.healthlake.UntagResource(ctx, req.ResourceARN, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsForResourceRequest struct {
	ResourceARN string `json:"ResourceARN"`
}

type listTagsForResourceResponse struct {
	Tags []tagJSON `json:"Tags"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsForResourceRequest) (any, error) {
		tags, err := h.healthlake.ListTagsForResource(ctx, req.ResourceARN)
		if err != nil {
			return nil, err
		}

		return listTagsForResourceResponse{Tags: tagsToWire(tags)}, nil
	})
}
