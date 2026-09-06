package athena

import (
	"context"
	"net/http"
	"sort"
)

// tagsToMap folds a wire tag list into a map (last value wins on a repeated key).
func tagsToMap(in []tagJSON) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, t := range in {
		out[t.Key] = t.Value
	}

	return out
}

type tagResourceRequest struct {
	ResourceARN string    `json:"ResourceARN"`
	Tags        []tagJSON `json:"Tags"`
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *tagResourceRequest) (any, error) {
		if err := h.athena.TagResource(ctx, req.ResourceARN, tagsToMap(req.Tags)); err != nil {
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
		if err := h.athena.UntagResource(ctx, req.ResourceARN, req.TagKeys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listTagsForResourceRequest struct {
	ResourceARN string `json:"ResourceARN"`
	NextToken   string `json:"NextToken"`
	MaxResults  int32  `json:"MaxResults"`
}

type listTagsForResourceResponse struct {
	Tags      []tagJSON `json:"Tags"`
	NextToken string    `json:"NextToken,omitempty"`
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listTagsForResourceRequest) (any, error) {
		tags, err := h.athena.ListTagsForResource(ctx, req.ResourceARN)
		if err != nil {
			return nil, err
		}

		keys := make([]string, 0, len(tags))
		for k := range tags {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		out := make([]tagJSON, 0, len(keys))
		for _, k := range keys {
			out = append(out, tagJSON{Key: k, Value: tags[k]})
		}

		return listTagsForResourceResponse{Tags: out}, nil
	})
}
