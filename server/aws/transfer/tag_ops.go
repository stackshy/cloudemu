package transfer

import (
	"context"
	"net/http"
)

type tagResourceRequest struct {
	Arn  string    `json:"Arn"`
	Tags []tagJSON `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.transfer.TagResource(ctx, req.Arn, tagsToMap(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type untagResourceRequest struct {
	Arn     string   `json:"Arn"`
	TagKeys []string `json:"TagKeys"`
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *untagResourceRequest) (any, error) {
		if err := h.transfer.UntagResource(ctx, req.Arn, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsForResourceRequest struct {
	Arn        string `json:"Arn"`
	NextToken  string `json:"NextToken"`
	MaxResults int32  `json:"MaxResults"`
}

type listTagsForResourceResponse struct {
	Arn       string    `json:"Arn"`
	Tags      []tagJSON `json:"Tags"`
	NextToken string    `json:"NextToken,omitempty"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsForResourceRequest) (any, error) {
		tags, err := h.transfer.ListTagsForResource(ctx, req.Arn)
		if err != nil {
			return nil, err
		}

		return listTagsForResourceResponse{Arn: req.Arn, Tags: mapToTags(tags)}, nil
	})
}
