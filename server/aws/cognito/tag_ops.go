package cognito

import (
	"context"
	"net/http"
)

type tagResourceRequest struct {
	ResourceARN string            `json:"ResourceArn"`
	Tags        map[string]string `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.cognito.TagResource(ctx, req.ResourceARN, req.Tags); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceRequest struct {
	ResourceARN string   `json:"ResourceArn"`
	TagKeys     []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.cognito.UntagResource(ctx, req.ResourceARN, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsForResourceRequest struct {
	ResourceARN string `json:"ResourceArn"`
}

type listTagsForResourceResponse struct {
	Tags map[string]string `json:"Tags"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsForResourceRequest) (any, error) {
		tags, err := h.cognito.ListTagsForResource(ctx, req.ResourceARN)
		if err != nil {
			return nil, err
		}

		return listTagsForResourceResponse{Tags: tags}, nil
	})
}
