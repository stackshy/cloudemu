package ssm

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// parameterTagger is the AWS-specific parameter-tagging surface. It's not part
// of the portable ParameterStore driver, so the handler type-asserts for it.
type parameterTagger interface {
	TagParameter(ctx context.Context, name string, tags map[string]string) error
	UntagParameter(ctx context.Context, name string, keys []string) error
	ListParameterTags(ctx context.Context, name string) (map[string]string, error)
}

type ssmTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func (h *Handler) addTagsToResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.store.(parameterTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceType string   `json:"ResourceType"`
		ResourceID   string   `json:"ResourceId"`
		Tags         []ssmTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		tags[t.Key] = t.Value
	}

	if err := tagger.TagParameter(r.Context(), req.ResourceID, tags); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) removeTagsFromResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.store.(parameterTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceType string   `json:"ResourceType"`
		ResourceID   string   `json:"ResourceId"`
		TagKeys      []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := tagger.UntagParameter(r.Context(), req.ResourceID, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.store.(parameterTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		ResourceType string `json:"ResourceType"`
		ResourceID   string `json:"ResourceId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := tagger.ListParameterTags(r.Context(), req.ResourceID)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]ssmTag, 0, len(tags))
	for k, v := range tags {
		out = append(out, ssmTag{Key: k, Value: v})
	}

	wire.WriteJSON(w, map[string]any{"TagList": out})
}
