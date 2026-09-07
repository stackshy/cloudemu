package aoss

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
	ResourceArn string    `json:"resourceArn"`
	Tags        []tagJSON `json:"tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.aoss.TagResource(ctx, req.ResourceArn, tagsFromWire(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn"`
	TagKeys     []string `json:"tagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.aoss.UntagResource(ctx, req.ResourceArn, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn"`
}

type listTagsForResourceResponse struct {
	Tags []tagJSON `json:"tags"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsForResourceRequest) (any, error) {
		tags, err := h.aoss.ListTagsForResource(ctx, req.ResourceArn)
		if err != nil {
			return nil, err
		}

		return listTagsForResourceResponse{Tags: tagsToWire(tags)}, nil
	})
}
